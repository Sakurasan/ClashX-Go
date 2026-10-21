package main

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/smallnest/uix/components/empty"
	"github.com/smallnest/uix/components/segmented"
	"github.com/smallnest/uix/components/table"

	"clashx-go/core"
)

// connRefreshInterval is how often the visible connections page re-reads the
// core. One second matches the traffic sample rate and stays cheap.
const connRefreshInterval = time.Second

// rulesRefreshInterval re-reads the rule list only while it is shown; rules
// change only when the config reloads.
const rulesRefreshInterval = 30 * time.Second

// connectionsLoop polls the core while the settings window shows the
// connections tab. The fetch runs off the main thread; its result arrives
// through Window.Update, so the view never races it.
func (a *app) connectionsLoop() {
	t := time.NewTicker(connRefreshInterval)
	defer t.Stop()
	for range t.C {
		if a.quitting {
			return
		}
		a.win.Update(func() {
			if !a.win.IsVisible() {
				return
			}
			a.panel.pollConnections()
		})
	}
}

// pollConnections runs on the main thread: it reads the view state, starts
// at most one fetch, and lets the result land in the same thread.
func (p *panelApp) pollConnections() {
	if p.tab != tabConnections || p.connFetching {
		return
	}
	a := p.app
	if !a.ctrl.State().Running {
		p.conns = nil
		p.connErr = ""
		p.rules = nil
		p.rulesAt = time.Time{}
		return
	}
	wantRules := p.connView == connViewRules &&
		!p.rulesFetching &&
		time.Since(p.rulesAt) > rulesRefreshInterval
	client := a.ctrl.Client()
	p.connFetching = true
	if wantRules {
		p.rulesFetching = true
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conns, err := client.GetConnections(ctx)
		var rules []core.Rule
		var rulesErr error
		if wantRules {
			rules, rulesErr = client.GetRules(ctx)
		}
		a.win.Update(func() {
			p.connFetching = false
			if err != nil {
				p.connErr = err.Error()
			} else {
				p.connErr = ""
				// Newest first, as the dashboards list them.
				sort.SliceStable(conns.Connections, func(i, j int) bool {
					return conns.Connections[i].Start.After(conns.Connections[j].Start)
				})
				p.conns = conns
			}
			if wantRules {
				p.rulesFetching = false
				if rulesErr == nil {
					p.rules = rules
					p.rulesAt = time.Now()
				}
			}
		})
	}()
}

// connectionsPane is the native diagnostics page: the live connection table
// and the rule list the core matches against.
func (p *panelApp) connectionsPane(c *ui.Context) {
	a := p.app
	ui.Column(c).FillWidth().Grow(1).Gap(8).Children(func() {
		ui.Row(c).FillWidth().Children(func() {
			segmented.Segmented(c, segmented.Props{
				Selected: &p.connView,
				Labels:   []string{T("活动连接"), T("规则列表")},
				Label:    T("连接视图"),
			}).Width(230)
		})
		if !a.ctrl.State().Running {
			empty.Empty(c, empty.Props{
				Title:       T("核心未运行"),
				Description: T("启动核心后可在这里查看实时连接与规则。"),
			})
			return
		}
		if p.connView == connViewRules {
			p.rulesView(c)
			return
		}
		p.activeConnsView(c)
	})
}

// activeConnsView lists the live connections with the rule that matched
// each one, the proxy chain carrying it and its transferred bytes.
func (p *panelApp) activeConnsView(c *ui.Context) {
	t := c.Theme()
	a := p.app
	rows := p.filteredConnections()
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.TextInput(c, &p.connFilter).Grow(1).Placeholder(T("搜索主机、进程或规则…")).
			OnChange(func() { p.connSel = -1 })
		nativeButton(c, T("关闭所有连接"), buttonSecondary, func() { go a.closeConnections() })
	})
	var total int
	var down, up int64
	if p.conns != nil {
		total = len(p.conns.Connections)
		down, up = p.conns.DownloadTotal, p.conns.UploadTotal
	}
	summary := fmt.Sprintf(T("活动连接 %d · 下载 %s · 上传 %s"), total, humanBytes(down), humanBytes(up))
	if p.connFilter != "" {
		summary += fmt.Sprintf(T(" · 匹配 %d"), len(rows))
	}
	ui.Text(c, summary).FontSize(11).TextColor(t.TextMuted)
	if p.connErr != "" {
		ui.Text(c, T("读取连接失败: ")+p.connErr).FontSize(11).TextColor(t.Danger).SingleLine()
	}
	if p.conns == nil {
		empty.Empty(c, empty.Props{Title: T("正在读取连接…")})
		return
	}
	if len(rows) == 0 {
		empty.Empty(c, empty.Props{
			Title:       T("暂无匹配的连接"),
			Description: T("有新的连接时会自动出现在这里。"),
		})
		return
	}
	p.connTable.Selected = &p.connSel
	p.connTable.Key = func(row int) any { return rows[row].ID }
	cols := []ui.TableColumn{
		{Title: T("主机"), MinWidth: 120},
		{Title: T("匹配规则"), Width: 110, MinWidth: 70},
		{Title: T("链路"), Width: 90, MinWidth: 60},
		{Title: T("流量"), Width: 124, MinWidth: 80, Align: ui.End},
	}
	submitted := table.Table(c, table.Props{
		State:   &p.connTable,
		Columns: cols,
		Rows:    len(rows),
		Cell: func(row, col int) {
			conn := rows[row]
			switch col {
			case 0:
				ui.Text(c, connHost(conn)).SingleLine()
			case 1:
				ui.Text(c, connRule(conn)).SingleLine()
			case 2:
				ui.Text(c, connChain(conn)).SingleLine()
			default:
				ui.Text(c, fmt.Sprintf("↓%s ↑%s",
					compactBytes(conn.Download), compactBytes(conn.Upload))).FontSize(11).SingleLine()
			}
		},
	}).Grow(1).Submitted()
	if submitted && p.connSel >= 0 && p.connSel < len(rows) {
		conn := rows[p.connSel]
		go p.showConnDetail(conn)
	}
	ui.Text(c, T("双击一行查看连接详情")).FontSize(10).TextColor(t.TextMuted)
}

// rulesView lists the rules in the order the core matches them, with a
// filter for finding which rule sends a domain somewhere.
func (p *panelApp) rulesView(c *ui.Context) {
	t := c.Theme()
	rules := p.filteredRules()
	ui.TextInput(c, &p.ruleFilter).FillWidth().Placeholder(T("搜索规则…"))
	ui.Text(c, fmt.Sprintf(T("共 %d 条规则"), len(rules))).FontSize(11).TextColor(t.TextMuted)
	if len(p.rules) == 0 {
		empty.Empty(c, empty.Props{Title: T("正在读取规则…")})
		return
	}
	if len(rules) == 0 {
		empty.Empty(c, empty.Props{Title: T("暂无匹配的规则")})
		return
	}
	cols := []ui.TableColumn{
		{Title: T("类型"), Width: 130, MinWidth: 70},
		{Title: T("内容"), MinWidth: 120},
		{Title: T("策略"), Width: 140, MinWidth: 80},
	}
	table.Table(c, table.Props{
		State:   &p.ruleTable,
		Columns: cols,
		Rows:    len(rules),
		Cell: func(row, col int) {
			rule := rules[row]
			switch col {
			case 0:
				ui.Text(c, rule.Type).SingleLine()
			case 1:
				ui.Text(c, orDash(rule.Payload)).SingleLine()
			default:
				ui.Text(c, orDash(rule.Proxy)).SingleLine()
			}
		},
	}).Grow(1)
}

// filteredConnections applies the search box to host, process, rule and
// chain; an empty query keeps the newest-first order from the core.
func (p *panelApp) filteredConnections() []core.Connection {
	if p.conns == nil {
		return nil
	}
	all := p.conns.Connections
	q := strings.ToLower(strings.TrimSpace(p.connFilter))
	if q == "" {
		return all
	}
	out := make([]core.Connection, 0, len(all))
	for _, conn := range all {
		hay := strings.ToLower(strings.Join([]string{
			connHost(conn),
			conn.Metadata.Process,
			conn.Metadata.ProcessPath,
			conn.Rule,
			conn.RulePayload,
			strings.Join(conn.Chains, " "),
		}, " "))
		if strings.Contains(hay, q) {
			out = append(out, conn)
		}
	}
	return out
}

// filteredRules applies the search box to type, payload and policy.
func (p *panelApp) filteredRules() []core.Rule {
	q := strings.ToLower(strings.TrimSpace(p.ruleFilter))
	if q == "" {
		return p.rules
	}
	out := make([]core.Rule, 0, len(p.rules))
	for _, rule := range p.rules {
		hay := strings.ToLower(rule.Type + " " + rule.Payload + " " + rule.Proxy)
		if strings.Contains(hay, q) {
			out = append(out, rule)
		}
	}
	return out
}

// connHost names the connection's destination: the SNI/host when the core
// sniffed one, otherwise the IP, always with its port.
func connHost(conn core.Connection) string {
	m := conn.Metadata
	host := m.Host
	if host == "" {
		host = m.SniffHost
	}
	if host == "" {
		host = m.DestinationIP
	}
	if host == "" {
		return "—"
	}
	if m.DestinationPort != "" {
		host = net.JoinHostPort(host, m.DestinationPort)
	}
	return host
}

func connRule(conn core.Connection) string {
	if conn.RulePayload != "" {
		return conn.RulePayload
	}
	return orDash(conn.Rule)
}

func connChain(conn core.Connection) string {
	if len(conn.Chains) == 0 {
		return "—"
	}
	return strings.Join(conn.Chains, " → ")
}

func connProcess(conn core.Connection) string {
	if conn.Metadata.ProcessPath != "" {
		return conn.Metadata.ProcessPath
	}
	if conn.Metadata.Process != "" {
		return conn.Metadata.Process
	}
	return "—"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// compactBytes saves the space in a table cell: 1.2KB rather than 1.2 KB.
func compactBytes(n int64) string {
	return strings.ReplaceAll(humanBytes(n), " ", "")
}

// showConnDetail opens a read-only dialog with the parts the table leaves
// out: endpoints, process, matched rule type and start time.
func (p *panelApp) showConnDetail(conn core.Connection) {
	m := conn.Metadata
	source := "—"
	if m.SourceIP != "" {
		source = net.JoinHostPort(m.SourceIP, m.SourcePort)
	}
	lines := []string{
		fmt.Sprintf("%s: %s · %s", T("类型"), orDash(m.Type), orDash(m.Network)),
		fmt.Sprintf("%s: %s", T("来源"), source),
		fmt.Sprintf("%s: %s", T("进程"), connProcess(conn)),
		fmt.Sprintf("%s: %s (%s)", T("匹配规则"), orDash(conn.Rule), orDash(conn.RulePayload)),
		fmt.Sprintf("%s: %s", T("链路"), connChain(conn)),
		fmt.Sprintf("%s: %s (DNS %s)", T("目标地址"),
			orDash(m.DestinationIP), orDash(m.DNSMode)),
		fmt.Sprintf("%s: %s", T("开始"), conn.Start.Local().Format("2006-01-02 15:04:05")),
		fmt.Sprintf("%s: ↓ %s · ↑ %s", T("流量"),
			humanBytes(conn.Download), humanBytes(conn.Upload)),
	}
	_, _ = mygo.Dialog.Message(mygo.MessageOptions{
		Type:    mygo.MessageInfo,
		Title:   T("连接详情"),
		Message: connHost(conn),
		Detail:  strings.Join(lines, "\n"),
	})
}
