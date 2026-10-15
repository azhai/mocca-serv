# ADR 0007: 自研 lib/web + lib/mq，弃用 labstack/echo

## Status

Accepted（2026-10-15，用户决策「建库 + 把 echo 全换掉」；MQ 形态 = 进程内 pub/sub + 跨进程共享内存）

## Context

mocca 只用到了 echo v5 极小的一面（实测 42 个文件 / 134 处 `echo.`，去重后只有 6 个符号：
`Context`/`Echo`/`HandlerFunc`/`MiddlewareFunc`/`New` + 一处注释里的 `HTTPError`；verb 只有
GET/POST/DELETE/Any/Use/Group/Router/ServeHTTP）。全仓无 `bind:""`/`form:""`/`query:""` tag、
无 405 依赖、无 echo JWT 中间件、静态页走 `web.Register` 自写。echo 却拖进一批用不到的间接面。

同时需要一个消息队列能力：现有的 `mediaindex/danmaku_hub.go`（SSE 扇出）与索引增量通知就是
「topic 多订阅、每订阅一条有界队列、满则丢、取消即清理」的形状；参考 molly 的 `event_bus.odin`
（每订阅一根 backlog chan、Publish 非阻塞 try_send 满了就地丢）与 gobus（但 mocca 只用其 environ，
bus 从未用过）。目标还有跨进程：本机高频、丢几条无所谓的场景走共享内存，需可靠回放走文件。

## Decision

在 `lib/web` 与 `lib/mq` 自研两个最小库，并把整个仓库从 echo 迁到 `lib/web`。

- **lib/web**：段树路由（静态 > `:param` > `*wild`）、Group 前缀 + 中间件继承、洋葱中间件
  （全局在外、路由在内）、`Context` 只实现真实用到的那 12 个方法、JSON 优先的 Bind、
  SSE 友好的 `Response`（覆盖 WriteHeader/Write/Flush，Flushing 内层不支持即空操作）。
  刻意差异：处理器 error 一律交 `ErrorHandler`，不回 echo 的 `*echo.HTTPError`（本仓一处没用）。
- **lib/mq**：
  - `Bus` 进程内 topic 扇出，每订阅一根有界 chan，满了按策略 DropNewest/DropOldest/Block，
    发布方永不被慢读者拖住；数据 chan 不关、用 done chan 发结束信号（避开 send-on-closed panic）。
  - `Journal` 跨平台文件追加队列（含 Windows），读方自带游标，重开续号并截断崩溃脏尾。
  - `Shm` unix mmap 定长槽序号环（单写多读无锁，`x/sys/unix` + `unsafe` 原子）；`!unix` 编译桩指回 Journal。
  - 刻意不做：ack/重发/持久化确认、topic 通配匹配、优先级（gobus 有，代价是两个依赖 + 每 topic 一条有序队列）。

## Consequences

- **收益**：去掉 `labstack/echo/v5` 直接依赖；HTTP 面与队列面变成可读、可改、可测的本仓代码
  （lib/web 85.6%、lib/mq 82.8% 覆盖，-race 绿）；跨进程能力按本机语义设计，不背网络队列的重量。
- **代价**：路由/上下文/队列成了自维护代码，升级与兼容由本仓负责（不再有 echo 的安全更新与生态）。
  因此以**全量门禁**兜底：`go vet ./...`、`make test`(-race -coverpkg)、`make test-api`(noweb,nopgsql)、
  双 tag 编译、Windows 交叉编译、真机冒烟全绿。
- **接受的行为差异修正**：lib/web 的 `split` 早期过度归一化尾斜杠，令 `/admin` 与 `/admin/` 塌成同一
  节点、盖掉重定向；已改成「中间连续斜杠忽略、尾斜杠保留语义」，与 echo 默认一致。这类差异靠
  `routes_test`/`embed_test` 的既有断言暴露，不是靠人工比对 echo 源码保证。
- **天花板（ponytail 标注）**：Windows 无共享内存快路径，退回 Journal（吞吐低、无内存序保证，只做「能用」）；
  接入进度：`Bus` 已接进 `mediaindex/danmaku_hub.go`（topic=存储键+sha1，一条弹幕只 marshal 一次、
  多观看者共享字节，替掉了旧自建 hub 的每连接重复序列化）；`Journal` 已接进 `mediaindex/danmaku_bridge.go`
  作**多实例弹幕跨进程桥**（默认关，配 `DANMAKU_JOURNAL_DIR` 才开）：受 Journal 单写者约束，每个实例写
  自己那份 `danmaku-<pid>-<rand>.journal`、只 tail 别人的（天然无回环、无需 origin 标记；首次发现即移到末尾只投新不重放）。
  单进程部署热路径零成本（bridge 为 nil 时 PublishDanmaku 的落盘是 no-op）。`Shm` 仍是已就绪、已测但尚无真实消费者的能力。
- 与 ADR 0006 不冲突：Go 仍是权威实现，本次转写评估未变；lib/web/lib/mq 是 Go 库替换 Go 库，不是转 Odin。
