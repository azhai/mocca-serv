# lib/mq —— 最小消息队列

两种队列形态，按需选一种；全部零第三方依赖（跨进程侧只在 unix 用 `x/sys/unix` + `unsafe` 原子）。

| 形态 | 用途 | 平台 | 落盘 |
| --- | --- | --- | --- |
| `Bus` | 进程内 topic 扇出（pub/sub） | 全部 | 否 |
| `Journal` | 跨进程文件追加队列 | 全部（含 Windows） | 是 |
| `Shm` | 跨进程 mmap 定长槽序号环 | unix（`!unix` 是编译桩，指回 Journal） | 否 |

## Bus（进程内）

每个订阅者一根**有界** chan；发布方永不因某个慢读者被拖住（满了按策略丢，可查计数）。

```go
bus := mq.New(mq.Options{Backlog: 32, Full: mq.DropNewest})

sub, _ := bus.Subscribe("topic")     // 用完必须 Cancel，否则计入 MaxSubs、内存只涨不降
defer sub.Cancel()

bus.Publish("topic", payload)        // 返回收到的订阅者数；编码一次、多读者共享同一份字节
for m, ok := sub.Next(); ok; m, ok = sub.Next() {
    // m.Body 是已编码好的字节，直接用（SSE 侧不必再 marshal）
}
```

- **满策略 `Full`**：`DropNewest`（丢正在发的这条，默认，不扰动读者已排队顺序）/ `DropOldest`（丢队首腾位给最新）/ `Block`（阻塞发布方直到有位，一个卡死的读者会拖住整个发布方）。
- **结束信号与数据分离**：数据 chan 全程**不关**（发布方可能还握着快照，send-on-closed 会 panic），`Cancel`/`Close` 只关 `Done()`。别 `range C()`，用 `Next()` 或自己 `select` 两根。
- `Publish` 的编码规则：`nil`→空 body；`[]byte`/`string`/`json.RawMessage`→原样字节；其余走 `encoding/json`。
- `Options` 零值可用：`Backlog` 默认 16，`MaxSubs` 默认 4096，`Full` 默认 `DropNewest`。

## Journal（跨进程 · 文件）

磁盘记录形状：`[8B seq][4B frameLen][frame]`，`frame = [2B topicLen][topic][body]`。读方各自带字节游标。

```go
w, _ := mq.OpenJournal("queue.journal")   // 续末尾编号；崩溃留下的半条脏尾自动截断
w.Append("topic", payload)                 // 返回 seq

r, _ := mq.OpenJournalReader("queue.journal", "") // topic 传 "" 读全部，非空则本地过滤
r.SeekTo(100)                              // 跳到 seq>=100（"重连后补历史"，低频）
for {
    m, ok := r.Next()                      // 读到当前末尾即 ok=false（非阻塞）
    if !ok { break }                       // 慢读者掉队只丢自己的，不影响写者
    _ = m.Topic
}
```

- **单写者约束**：同一进程内 `Append` 由一把锁串行；跨进程的「谁在写」由调用方保证（一个文件一个写者），本库**不做文件锁**——多写者会交错写坏记录头。要跨多进程写，就**每进程一个文件**（见下面的弹幕桥）。

## Shm（跨进程 · unix mmap）

定长槽序号环，单写多读无锁：发布方清有效位→写载荷→翻有效位→推 `head`；读方带游标校验 seq，掉队就跳过（`Dropped()` 可查）。`!unix` 平台 `OpenShm` 直接返回「不支持」错误，逼调用方改用 Journal。

## 刻意不做（都写进代码注释，不藏）

- **ack / 重发 / 持久化确认**：本库真实消费者是 SSE 扇出与索引通知，丢一条的后果是「少显示一条弹幕」，不是「数据不一致」。
- **topic 通配匹配**：跨进程侧的过滤在读者本地做，量级用不上路由表。
- **优先级**：需要每 topic 一条有序待分队列 + 唤醒线程，代价远超收益。

## 真实消费者（本仓库）

- **`mediaindex/danmaku_hub.go`**：弹幕实时推送用 `Bus`（topic = 存储键+sha1，`Backlog=32 DropNewest`），一条弹幕只 marshal 一次、多观看者共享字节。
- **`mediaindex/danmaku_bridge.go`**：多实例弹幕跨进程桥，用 `Journal`，**默认关**（配 `DANMAKU_JOURNAL_DIR` 才开）。正是绕开单写者约束的形状——每实例只写自己那份 `danmaku-<pid>-*.journal`、只 tail 别人的，于是天然无回环、无需 origin 标记；首次发现某文件即把游标移到末尾，只投新消息不重放历史。
