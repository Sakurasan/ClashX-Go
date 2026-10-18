package core

import (
	"context"
	"sort"
	"sync"
	"time"
)

// State is the snapshot of the core that the UI reads. It is updated on
// the main thread; the controller mutates it from polls and hands it to
// the view. Fields are plain so a view can read them without locking.
type State struct {
	Running  bool
	Version  string
	Mode     string
	Selected map[string]string // group name -> chosen member
	Groups   []*GroupView
	Traffic  Traffic
	Memory   int64 // bytes in use
	Level    string
	CorePath string
	Log      []string

	// Subscription status, refreshed with the poll loop. SubProxies is the
	// node count of the downloaded subscription (0 when none). SubInfo is
	// the provider's quota header, nil when it sent none.
	SubProxies     int
	SubUpdated     time.Time
	SubDownloading bool
	SubInfo        *SubscriptionInfo
}

// GroupView is the UI-ready form of a policy group.
type GroupView struct {
	Name     string
	Type     string
	Current  string
	Members  []string
	Delays   map[string]int // member name -> ms; -1 means unreachable
	TestedAt int64
}

// Controller owns the core lifecycle and the state the view draws. It
// keeps one goroutine polling the controller and another streaming
// traffic, coalescing updates into State and calling onChange (on the
// main thread) to repaint.
type Controller struct {
	mgr    *Manager
	client *Client
	cfg    Config

	// groupOrder is the proxy-groups order read from the YAML the running
	// core loaded, so the UI lists groups like ClashX does (the API's JSON
	// map has no order).
	groupOrder []string

	// delays caches full group test results (group→member→ms): the poll
	// loop rebuilds GroupViews and the API only reports history for each
	// group's current member, so without this the other nodes go blank.
	delays map[string]map[string]int

	mu       sync.Mutex
	state    State
	onChange func(State)

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// NewController builds a controller from a manager and a config.
func NewController(mgr *Manager, cfg Config) *Controller {
	c := &Controller{
		mgr:    mgr,
		cfg:    cfg,
		client: NewClient(cfg.ControllerPort, cfg.Secret),
		state: State{
			Mode:     cfg.Mode,
			Selected: map[string]string{},
			Groups:   []*GroupView{},
			CorePath: mgr.Binary(),
		},
	}
	return c
}

// Client exposes the controller client (for settings to reuse).
func (c *Controller) Client() *Client { return c.client }

// Manager exposes the underlying manager.
func (c *Controller) Manager() *Manager { return c.mgr }

// Config returns the configuration the controller was built with.
func (c *Controller) Config() Config { return c.cfg }

// OnChange registers the callback run (on the main thread) when state
// changes. Only one callback is used.
func (c *Controller) OnChange(fn func(State)) { c.onChange = fn }

// State returns a copy of the current state.
func (c *Controller) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// emit publishes a mutated state to the callback.
func (c *Controller) emit() {
	c.mu.Lock()
	s := c.state
	c.mu.Unlock()
	if c.onChange != nil {
		c.onChange(s)
	}
}

// Start boots the core: ensure the binary, write config, launch, then
// begin polling and traffic streaming.
func (c *Controller) Start(ctx context.Context) error {
	if _, err := c.mgr.EnsureBinary(); err != nil {
		return err
	}
	if configPath, err := c.mgr.Start(ctx, c.cfg, func() error {
		return c.client.Ping(context.Background())
	}); err != nil {
		return err
	} else {
		c.groupOrder = GroupOrder(configPath)
	}
	c.setState(func(s *State) {
		s.Running = true
		s.Version = c.mgr.Version()
		s.SubProxies = c.mgr.SubscriptionProxyCount()
		s.SubUpdated = c.mgr.SubscriptionUpdatedAt()
		s.SubInfo = c.mgr.SubscriptionInfo()
	})
	c.beginLoops(ctx)
	return nil
}

// Stop halts the core and stops the loops.
func (c *Controller) Stop() {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	c.mgr.Stop()
	c.setState(func(s *State) { s.Running = false })
}

// setState mutates state under lock and emits.
func (c *Controller) setState(mut func(*State)) {
	c.mu.Lock()
	mut(&c.state)
	c.mu.Unlock()
	c.emit()
}

// beginLoops starts polling and streaming goroutines.
func (c *Controller) beginLoops(ctx context.Context) {
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	go c.pollLoop(c.ctx)
	go c.trafficLoop(c.ctx)
	go c.testLoop(c.ctx)
	go func() {
		<-c.ctx.Done()
		close(c.done)
	}()
}

// pollLoop refreshes configs, proxies and memory a few times a second.
func (c *Controller) pollLoop(ctx context.Context) {
	subCheckedAt := time.Time{}
	refreshSub := func() {
		// Reading and scanning the subscription file is cheap but not free
		// (it can hold thousands of nodes), so refresh it at most every 30s.
		if time.Since(subCheckedAt) < 30*time.Second {
			return
		}
		subCheckedAt = time.Now()
		n := c.mgr.SubscriptionProxyCount()
		at := c.mgr.SubscriptionUpdatedAt()
		info := c.mgr.SubscriptionInfo()
		c.setState(func(s *State) {
			s.SubProxies = n
			s.SubUpdated = at
			s.SubInfo = info
		})
	}
	fails := 0
	noteFailure := func() {
		// The API can blip during a core reload; only a sustained failure
		// means the process died behind our back (e.g. a second app
		// instance's cleanup). Then the menu must show 启动核心 again.
		fails++
		if fails == 3 {
			c.setState(func(s *State) { s.Running = false })
		}
	}
	refresh := func() {
		cfg, err := c.client.GetConfigs(ctx)
		if err != nil {
			noteFailure()
			return
		}
		proxies, err := c.client.GetProxies(ctx)
		if err != nil {
			noteFailure()
			return
		}
		fails = 0
		c.applyProxies(cfg.Mode, proxies)
		refreshSub()
	}
	refresh()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

// trafficLoop streams /traffic and updates the sample shown in the UI.
func (c *Controller) trafficLoop(ctx context.Context) {
	_ = c.client.StreamTraffic(ctx, func(t Traffic) {
		c.setState(func(s *State) { s.Traffic = t })
	})
}

// SelectProxy chooses a member of a group and updates the local state.
func (c *Controller) SelectProxy(ctx context.Context, group, member string) error {
	if err := c.client.SelectProxy(ctx, group, member); err != nil {
		return err
	}
	c.setState(func(s *State) {
		if s.Selected == nil {
			s.Selected = map[string]string{}
		}
		s.Selected[group] = member
		for _, g := range s.Groups {
			if g.Name == group {
				g.Current = member
			}
		}
	})
	// ClashX shows a delay for the node you just picked: measure it right
	// after the switch instead of waiting for the next full test round.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		d, err := c.client.TestProxyDelay(ctx, member)
		if err != nil {
			d = -1
		}
		c.setState(func(s *State) {
			if c.delays[group] == nil {
				c.delays[group] = map[string]int{}
			}
			c.delays[group][member] = d
			for _, g := range s.Groups {
				if g.Name == group {
					g.Delays[member] = d
				}
			}
		})
	}()
	return nil
}

// SetMode changes the global mode and reflects it locally.
func (c *Controller) SetMode(ctx context.Context, mode string) error {
	if err := c.client.SetMode(ctx, mode); err != nil {
		return err
	}
	c.setState(func(s *State) { s.Mode = mode })
	return nil
}

// TestGroup runs a delay test on every member of a group and stores the
// results (name→ms, -1 for unreachable).
func (c *Controller) TestGroup(ctx context.Context, group string) error {
	c.mu.Lock()
	var members []string
	for _, g := range c.state.Groups {
		if g.Name == group {
			members = append([]string(nil), g.Members...)
		}
	}
	c.mu.Unlock()
	if len(members) == 0 {
		return nil
	}
	delays := c.client.TestGroupDelay(ctx, members)
	c.setState(func(s *State) {
		if c.delays == nil {
			c.delays = map[string]map[string]int{}
		}
		c.delays[group] = delays
		for _, g := range s.Groups {
			if g.Name == group {
				g.Delays = delays
				g.TestedAt = time.Now().Unix()
			}
		}
	})
	return nil
}

// testDelayRefresh is how often the running core is re-tested so the menu
// carries fresh delays without the user pressing 延迟测速.
const testDelayRefresh = 5 * time.Minute

// testLoop waits for the first group list, tests every group once, then
// refreshes on a timer. Groups are tested one after another so a large
// subscription does not flood the core with probes.
func (c *Controller) testLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if len(c.State().Groups) > 0 {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	for {
		for _, g := range c.State().Groups {
			if ctx.Err() != nil {
				return
			}
			cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			_ = c.TestGroup(cctx, g.Name)
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(testDelayRefresh):
		}
	}
}

// CloseConnections closes all active connections.
func (c *Controller) CloseConnections(ctx context.Context) error {
	return c.client.CloseConnections(ctx)
}

// RefreshSubscription re-downloads the subscription and, when the core is
// running, reloads providers so the new nodes take effect immediately. It
// returns the number of proxies in the freshly downloaded file.
func (c *Controller) RefreshSubscription(ctx context.Context) (int, error) {
	c.SetSubDownloading(true)
	n, err := c.mgr.RefreshSubscription(ctx, c.cfg)
	c.setState(func(s *State) {
		s.SubDownloading = false
		if err == nil {
			s.SubProxies = n
			s.SubUpdated = time.Now()
			s.SubInfo = c.mgr.SubscriptionInfo()
		}
	})
	if err != nil {
		return 0, err
	}
	if c.mgr.Running() {
		// The file provider is loaded at startup; ask the core to reload it
		// so the running config picks up the fresh nodes.
		_ = c.client.ReloadProvider(ctx, "provider")
	}
	return n, nil
}

// SetSubDownloading marks the state while a subscription download runs.
func (c *Controller) SetSubDownloading(on bool) {
	c.setState(func(s *State) { s.SubDownloading = on })
}

// applyProxies turns a /proxies snapshot into state: groups are ordered by
// the config file, seeded from mihomo's history for each current member and
// topped up from the delay cache so tested nodes survive between polls.
func (c *Controller) applyProxies(mode string, proxies *Proxies) {
	groups := []*GroupView{}
	sel := map[string]string{}
	for name, p := range proxies.ProxyGroups() {
		g := &GroupView{Name: name, Type: p.Type, Current: p.Now, Members: p.All, Delays: map[string]int{}}
		// Seed the delay map from the probe history that mihomo keeps for
		// the group's current member. A Delay of 0 means untested yet.
		if len(p.History) > 0 {
			g.Delays[p.Now] = int(p.History[len(p.History)-1].Delay)
			g.TestedAt = p.History[len(p.History)-1].Time.Unix()
		}
		groups = append(groups, g)
		sel[name] = p.Now
	}
	sortGroups(groups, c.groupOrder)
	c.setState(func(s *State) {
		for _, g := range groups {
			for n, d := range c.delays[g.Name] {
				if _, ok := g.Delays[n]; !ok {
					g.Delays[n] = d
				}
			}
		}
		s.Running = true
		s.Mode = mode
		s.Selected = sel
		s.Groups = groups
	})
}

// sortGroups orders groups by the config file declaration (order), then
// alphabetically, so repeated polls keep a stable, ClashX-like list.
func sortGroups(groups []*GroupView, order []string) {
	idx := make(map[string]int, len(order))
	for i, name := range order {
		if _, dup := idx[name]; !dup {
			idx[name] = i
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		gi, oki := idx[groups[i].Name]
		gj, okj := idx[groups[j].Name]
		switch {
		case oki && okj:
			return gi < gj
		case oki:
			return true
		case okj:
			return false
		default:
			return groups[i].Name < groups[j].Name
		}
	})
}
