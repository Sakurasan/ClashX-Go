<p align="center">
<img src="assets/icon.png" alt="ClashX Go icon" width="100">
</p>
<h1 align="center">ClashX Go</h1>

[English](README.md) | **简体中文**

一个 macOS 菜单栏代理客户端，界面与行为尽量复刻
[ClashX](https://github.com/yichengchen/clashX) 与
[ClashX Meta](https://github.com/MetaCubeX/ClashX.Meta)：原生托盘菜单、策略组与
节点延迟角标、实时速率、ClashX 风格的偏好设置窗口。使用 Go +
[MyGo](https://mygo.egoist.dev) 开发，内核为 [mihomo](https://github.com/MetaCubeX/mihomo)。

整个应用是一个自包含的 `.app`——内核随包分发，运行时不下载任何东西。


## 功能

**原版风格的菜单栏**

- 界面支持简体中文 / English（跟随系统语言，可用 `CLASHX_GO_LANG=en` 强制英文）
- 原生托盘菜单：出站模式、策略组（节点延迟角标 `✅ / ⚠️ / ❌`）、切换节点、
  延迟测速、配置管理
- 菜单栏双行实时速率（`↑ / ↓`），固定宽度不抖动；系统代理与 TUN 都没开时
  整条状态项变浅
- 自定义图标，可在配置目录放 `menuImage.png` 自定义

**代理控制**

- 系统代理无需特权助手，开启前会快照原有代理设置，关闭/退出时原样还原
- TUN 模式：首次开启时一次性管理员授权，安装 setuid 内核副本，之后切换 TUN
  无需再输密码（热 API 调用）
- Tun DNS：开启 TUN 时可把系统 DNS 指向指定地址，关闭 TUN 或退出应用时自动
  还原；留空使用本地 DNS
- 按 App 分流：在「分流」页选择 `.app`，可把它的流量固定走代理或直连；规则排在
  域名规则之前（PROCESS-NAME + 应用包路径正则，子进程也能命中），TUN 与系统
  代理均生效
- 指定 WiFi SSID 自动暂停、核心停止后静默自愈重启、控制器端口被占用时自动顺延

**配置管理**

- 托管订阅：添加/更新/删除、按订阅标题命名、菜单显示流量与到期；也支持本地
  配置列表
- 完整 Clash 订阅会**保留自带的策略组、规则与 DNS**；纯节点列表则生成默认配置
- 秒切式热重载；当前配置文件被外部修改会自动重载；按配置记忆节点选择并在
  重载后回放

**偏好设置（对齐原版）**

- 六个页签：通用 / 连接 / 分流 / 全局快捷键 / 调试 / Meta
- 通用：开机启动、减少通知、IPv6、端口、API 密钥、允许局域网、延迟测试地址、
  WiFi 暂停列表、代理忽略列表
- 全局快捷键：系统代理、TUN、切换模式、复制命令、打开菜单/日志/控制台
- 内置仪表盘：mihomo 的 `external-ui` 在 `/ui` 提供 zashboard

**诊断与提醒**

- 「连接」页：实时连接、命中的规则、代理链路与流量，支持搜索、关闭全部和双击
  查看连接详情；规则列表按内核匹配顺序展示
- 调试页可导出脱敏诊断包：版本、端口、系统代理、监听进程与日志尾部
- 订阅流量低于 5% 或 72 小时内到期时每天提醒一次（「减少通知」可关闭）
- 启动后自动检查更新，点击通知可直接安装

## 环境要求

- macOS 11 及以上（Apple Silicon 或 Intel；用 `-platform darwin/universal`
  可打出同时原生支持两者的通用应用）
- 构建需要 Go 1.27+；MyGo CLI 已声明为 Go tool（`go tool mygo`）
- 构建时拉取内核需要 `curl` 与 `gunzip`

## 安装

从 [Releases 页面](https://github.com/Sakurasan/ClashX-Go/releases) 下载
`ClashX Go <版本号>.dmg`，打开后把 `ClashX Go.app` 拖进「应用程序」。

应用为 ad-hoc 签名（没有 Developer ID），首次启动可能被 Gatekeeper 拦截：
右键应用 → **打开**，或在「系统设置 → 隐私与安全性」中允许。

## 从源码构建

先为两种架构下载 mihomo 内核，再打包：

```sh
scripts/fetch-cores.sh                  # mihomo v1.19.32（arm64 + amd64）
scripts/build-dnshelper.sh              # 通用 Tun DNS 提权组件
go tool mygo build                      # -> build/darwin-<arch>/ClashX Go.app + .dmg
go tool mygo build -platform darwin/universal   # 通用版 .app + 通用内核
```

同时拉取仪表盘（zashboard，最终在 `/ui` 提供）。MyGo 会把
`resources/dashboard` 打进应用包；首次启动时应用会把它复制到配置目录，
供 mihomo 的 `external-ui` 使用。CI 会自动执行这一步。

日常开发：

```sh
go tool mygo dev        # 带热重载的开发构建
go build . && ./clashx-go   # 普通二进制，需要 resources/darwin-<arch>/mihomo
go test ./... && go vet ./...
```

> 说明：macOS 的登录项（开机启动）要求以真正的 `.app` 包运行，因此该开关只在
> MyGo 打包版中可用，裸二进制（开发模式）会提示不可用。

## 自动构建与更新

GitHub Actions（`.github/workflows/build.yml`）在每次 push / PR 时执行
`go vet`、`go tool mygo vet` 与测试，并构建通用版 `.app`；推送 `v*` 标签会
把 DMG（配置更新后还有签名更新清单）发布为 GitHub Release。每次 push 到
`main` 还会更新一个滚动的 **Nightly** 预发布版：不签名、不会成为「Latest
Release」，因此稳定版更新源不会受它影响。

应用内更新使用 MyGo 的签名更新器：

1. `go tool mygo keygen` 生成 `mygo-update.key`（私钥，切勿入库）与
   `mygo-update.pub`
2. 在 `mygo.json` 中填入公钥：
   `"updates": { "publicKey": "…", "github": "<owner>/<repo>" }`
3. 把私钥内容加为仓库 Secret：`MYGO_UPDATER_PRIVATE_KEY`
4. 修改 `mygo.json` 的 `version`，在 `CHANGELOG.md` 加 `## <version>` 小节
   （会作为更新说明），打 `vX.Y.Z` 标签，CI 会发布签名更新源

之后菜单 **帮助 → 检查更新…** 会检查最新 tag、展示更新说明并下载安装、重启。
未配置 `updates` 的构建会明确提示「当前构建未启用更新检查」。

## 使用

1. 启动应用：菜单栏出现猫图标，核心自动拉起。
2. 菜单 → **配置** → **托管配置 → 管理…** 添加订阅，或直接选择本地 YAML 配置。
3. 在菜单中打开 **系统代理** 或 **Tun 模式**；有代理路径生效时图标变为实色。
4. **更多设置 → 偏好设置…** 打开设置窗口（`⌘,`）。

### 文件位置

所有数据都在 `~/.config/clashx-go/`，与 ClashX Meta 的 `~/.config/clash.meta/`
相互独立：

| 文件 | 用途 |
| --- | --- |
| `app.json` | 应用设置、订阅列表与节点选择记忆 |
| `sub-*.yaml` | 下载的订阅文件 |
| `running.yaml` | 实际推送给内核的配置（叠加端口/TUN） |
| `config.yaml` | 纯节点订阅生成的配置 |
| `core.log` | mihomo 标准输出/错误 |
| `dashboard/` 或 `ui/` | 可选的 Web 仪表盘（`/ui`） |

## 实现要点

- 内核随 `.app` 分发；首次启动复制到配置目录，运行时不联网下载。
- 内核先用**最小引导配置**（端点、TUN、一条 DIRECT 兜底规则）启动，控制器
  应答后立刻用 `PUT /configs` 推送完整配置——启动约 200ms，热重载在后台
  完成，期间旧路由继续工作。
- 轮询/流式读取 `/configs`、`/proxies`、`/traffic`，合并成状态快照供托盘菜单
  与设置窗口使用。
- TUN 通过一次 `osascript` 授权安装 setuid 内核副本
  （`/Library/Application Support/clashx-go/mihomo`），无常驻助手，后续切换
  免密。

## 目录结构

```
main.go              生命周期、托盘、设置窗口、内核编排
menu.go              ClashX 风格托盘菜单（策略组、节点、配置、帮助）
panel.go             设置窗口外壳、页签与页面
settings.go          设置模型、持久化、通用页
shortcuts.go         全局快捷键注册
about.go             关于窗口
profiles.go          托管订阅
manage.go, subdlg.go 订阅管理窗口 / 添加对话框
systemproxy_*.go     系统代理快照、应用与还原
tray_speed.go        菜单栏位图：图标 + 双行速率
core/
  core.go            进程管理：内置内核、引导配置、TUN
  config.go          配置生成与叠加
  client.go          mihomo REST 客户端
  controller.go      状态轮询与流量流
scripts/             内核与仪表盘下载脚本
assets/              应用图标与菜单栏猫图标（内嵌）
```

## 致谢

- [ClashX](https://github.com/yichengchen/clashX) /
  [ClashX Meta](https://github.com/MetaCubeX/ClashX.Meta)（west2online 及贡献者）
  ——本项目复刻的界面、行为与图标资产来源
- [mihomo](https://github.com/MetaCubeX/mihomo) ——代理内核
- [MyGo](https://mygo.egoist.dev)（egoist）——原生 macOS UI 框架
- [uix](https://github.com/smallnest/uix) ——组件库
- [zashboard](https://github.com/Zephyruso/zashboard) ——Web 仪表盘

## Star 趋势

[![Star History Chart](https://api.star-history.com/svg?repos=Sakurasan/ClashX-Go&type=Date)](https://star-history.com/#Sakurasan/ClashX-Go&Date)

## 许可证

[AGPL-3.0](LICENSE)。与 ClashX Meta 保持一致——本项目复用了其图标与设计。
