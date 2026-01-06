# MIT 6.5840 Lab 3: Raft 分布式一致性算法实现报告

## 实验概述

本实验实现了 Raft 分布式一致性算法,构建了一个能够容忍故障的复制状态机。实验分为四个部分:

- **3A**: 领导者选举
- **3B**: 日志复制
- **3C**: 持久化
- **3D**: 日志压缩 (快照)

本报告重点分析 **3C (持久化)** 和 **3D (日志压缩)** 的技术细节与实现难点。

---

## 测试结果

所有测试均通过,包括带竞态检测的测试:

```
$ go test -race
Test (3C): basic persistence (reliable network)...        Passed
Test (3C): more persistence (reliable network)...         Passed
Test (3C): partitioned leader and one follower crash...   Passed
Test (3C): Figure 8 (reliable network)...                Passed
Test (3C): unreliable agreement (unreliable network)...   Passed
Test (3C): Figure 8 (unreliable)...                      Passed
Test (3C): churn (reliable network)...                   Passed
Test (3C): unreliable churn (unreliable network)...      Passed
Test (3D): snapshots basic (reliable network)...         Passed
Test (3D): install snapshots (disconnect)...             Passed
Test (3D): install snapshots (crash)...                  Passed
Test (3D): crash and restart all servers...              Passed
Test (3D): snapshot initialization after crash...        Passed
PASS
ok      6.5840/raft1    403.924s
```

---

## Part 3C: 持久化 (Persistence)

### 3C.1 问题背景

在分布式系统中,服务器可能随时崩溃并重启。为了确保系统能够从崩溃中恢复,Raft 要求某些状态必须**持久化保存**到稳定存储中。当服务器重启时,能够从持久化的状态中恢复,继续参与 Raft 协议。

根据 Raft 论文 Figure 2,以下状态需要持久化:

- **currentTerm**: 服务器已知的最新任期号
- **votedFor**: 当前任期内投票给的候选人 ID
- **logs**: 日志条目数组

### 3C.2 实现架构

#### 3C.2.1 持久化状态保存 (`persist()`)

位置: `raft.go:98-110`

```go
func (rf *Raft) persist() {
    writeBuffer := new(bytes.Buffer)
    encoder := labgob.NewEncoder(writeBuffer)
    encoder.Encode(rf.currentTerm)
    encoder.Encode(rf.votedFor)
    encoder.Encode(rf.logs)
    encoder.Encode(rf.lastIncludedIndex)  // 3D 新增
    encoder.Encode(rf.lastIncludedTerm)   // 3D 新增
    raftstate := writeBuffer.Bytes()
    rf.persister.Save(raftstate, rf.persister.ReadSnapshot())
}
```

**关键设计决策**:

1. **使用 `labgob` 编码器**: 而非 Go 标准的 `gob`,因为 `labgob` 会在编码小写字段名时报错,避免 RPC 序列化错误

2. **编码顺序必须固定**: 解码时必须按照**完全相同的顺序**读取,否则会导致数据错乱

3. **每次状态变化都调用 `persist()`**: 在以下位置调用持久化:
   - `RequestVote` RPC 处理器中:更新 `currentTerm` 或 `votedFor` 后
   - `AppendEntries` RPC 处理器中:发现更高 `Term` 或添加日志后
   - `StartElection()`:成为候选人并投票给自己后
   - 日志添加/修改后

#### 3C.2.2 持久化状态恢复 (`readPersist()`)

位置: `raft.go:114-147`

```go
func (rf *Raft) readPersist(data []byte) {
    if data == nil || len(data) < 1 {
        return
    }
    r := bytes.NewBuffer(data)
    d := labgob.NewDecoder(r)

    var currentTerm int
    var votedFor int
    var logs []Entry
    var lastIncludedIndex int  // 3D 新增
    var lastIncludedTerm int   // 3D 新增

    if d.Decode(&currentTerm) != nil ||
       d.Decode(&votedFor) != nil ||
       d.Decode(&logs) != nil ||
       d.Decode(&lastIncludedIndex) != nil ||
       d.Decode(&lastIncludedTerm) != nil {
        fmt.Printf("Error: Raft server %d failed to read persist data\n", rf.me)
    } else {
        rf.currentTerm = currentTerm
        rf.votedFor = votedFor
        rf.logs = logs
        rf.lastIncludedIndex = lastIncludedIndex
        rf.lastIncludedTerm = lastIncludedTerm
    }
}
```

**恢复时机**:
在 `Make()` 函数中,初始化所有易失性状态后,立即调用 `readPersist()` 恢复持久化状态:

```go
// Make() 函数中
rf.readPersist(persister.ReadRaftState())
rf.commitIndex = rf.lastIncludedIndex  // 恢复 commitIndex
rf.lastApplied = rf.lastIncludedIndex   // 恢复 lastApplied
```

### 3C.3 容易出错的点

#### 错误 0: 未处理过期 RPC 回复 (导致 nextIndex 震荡) ⭐⭐⭐

**问题重要性**: ⭐⭐⭐ 这是导致 Figure 8 (unreliable) 测试卡死的**最核心 Bug**,在不稳定网络环境中几乎必现。

**问题描述**:
在不可靠网络中,Leader 可能会先收到一个**旧的** AppendEntries 失败回复 (Conflict),然后再收到**新的**成功回复 (Success)。由于未检测回复的时效性,导致 `nextIndex` 前后跳变,无法收敛。

**场景复现** (时间线):

```
T1: Leader 发送 AppendEntries(PrevLogIndex=10)
    -> 网络延迟,消息卡在路上

T2: Leader 再次发送 AppendEntries(PrevLogIndex=5)
    -> Follower 收到,日志匹配,回复 Success
    -> Leader 收到 Success,更新 nextIndex = 11 ✅

T3: Follower 终于收到了那个旧的 PrevLogIndex=10 的请求
    -> Follower 检查:本地日志只有 Index 5,不匹配
    -> Follower 回复 Conflict(ConflictIndex=6)

T4: Leader 收到这个**过期的 Conflict 回复**
    -> Leader 错误地将 nextIndex 从 11 回退到 6 ❌
    -> Leader 发送 AppendEntries(PrevLogIndex=5)... (倒退了!)

T5: Leader 再次收到 Success,更新 nextIndex = 11

T6: 又有旧的 Conflict 回复到达...
    -> nextIndex 震荡: 11 → 6 → 11 → 6 → ...
    -> 日志永远无法同步完成!
```

**根本原因**:
RPC 回复的处理函数 (`handleAppendEntriesReply`) 缺少**回复时效性验证**,无法区分"当前请求的回复"和"过期请求的回复"。

**错误代码**:
```go
func (rf *Raft) handleAppendEntriesReply(server int, args AppendEntriesArgs, reply AppendEntriesReply) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    // ❌ 缺少时效性检查!

    if reply.Success {
        rf.nextIndex[server] = args.PrevLogIndex + len(args.Entries) + 1
    } else {
        // 即使这个回复是过期的,也会导致 nextIndex 错误回退
        rf.nextIndex[server] = reply.ConflictLogIndex
    }
}
```

**修复方案**:
在 `handleAppendEntriesReply` 开头添加**回复与当前状态匹配性检查**:

```go
func (rf *Raft) handleAppendEntriesReply(server int, args AppendEntriesArgs, reply AppendEntriesReply) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    // 【核心修复】：防止 nextIndex 震荡
    // 验证: args.PrevLogIndex == rf.nextIndex[server] - 1
    //
    // 逻辑推导:
    // 1. Leader 发送请求时: args.PrevLogIndex = rf.nextIndex[server] - 1
    // 2. 收到回复时: rf.nextIndex[server] 可能已被其他回复更新
    // 3. 如果不等式不成立 → 说明这是一个过期的回复 → 直接丢弃
    if args.PrevLogIndex != rf.nextIndex[server] - 1 {
        return
    }

    if reply.Term > rf.currentTerm {
        rf.currentTerm = reply.Term
        rf.state = Follower
        rf.votedFor = -1
        rf.persist()
        return
    }

    if reply.Success {
        rf.matchIndex[server] = args.PrevLogIndex + len(args.Entries)
        rf.nextIndex[server] = rf.matchIndex[server] + 1
    } else {
        if reply.ConflictLogIndex > 0 {
            if reply.ConflictLogIndex < rf.nextIndex[server] {
                rf.nextIndex[server] = reply.ConflictLogIndex
            }
        } else {
            rf.nextIndex[server] = 1
        }
    }
}
```

**代码位置**: `raft.go:802-804`

**修复原理**:
- **发送时刻**: `nextIndex[server] = X`,Leader 发送 `AppendEntries(PrevLogIndex = X-1)`
- **接收时刻**: 检查 `args.PrevLogIndex == nextIndex[server] - 1`
  - 如果成立 → 回复是新鲜的,正常处理
  - 如果不成立 → `nextIndex` 已被其他回复更新,此回复过期,丢弃

**效果**:
- **修复前**: Figure 8 (unreliable) 测试超时 (120s),`nextIndex` 持续震荡
- **修复后**: 测试在 31.7s 内通过,RPC 数量从数万次降低到 6866 次

**教训**:
在分布式系统中处理 RPC 回复时,必须假设网络会**乱序、延迟、重复**,所有回复处理逻辑都应该包含:
1. **时效性检查**: 验证回复是否对应当前状态
2. **幂等性**: 多次处理相同回复不应产生副作用
3. **防御性编程**: 任何异常回复都不应破坏系统状态

---

#### 错误 1: 编码/解码顺序不匹配

**问题**:
```go
// 编码顺序
encoder.Encode(rf.currentTerm)
encoder.Encode(rf.votedFor)
encoder.Encode(rf.logs)

// 解码顺序错误
d.Decode(&logs)        // ❌ 顺序错误!
d.Decode(&currentTerm)
d.Decode(&votedFor)
```

**后果**: 读取的数据完全错乱,`currentTerm` 可能被赋值为日志数组,导致后续逻辑崩溃。

**正确做法**: 使用注释或辅助函数确保顺序一致:

```go
// 定义辅助函数封装编码逻辑
func (rf *Raft) encodeState() []byte {
    w := new(bytes.Buffer)
    e := labgob.NewEncoder(w)
    e.Encode(rf.currentTerm)
    e.Encode(rf.votedFor)
    e.Encode(rf.logs)
    // ...
    return w.Bytes()
}
```

#### 错误 2: 遗漏持久化调用点

**问题**: 只在 `AppendEntries` 中添加日志后调用 `persist()`,但在 `RequestVote` 中更新 `currentTerm` 后忘记持久化。

**后果**:
1. 服务器崩溃后,可能以过期的 `currentTerm` 重启
2. 导致"幽灵候选人"问题:同一个 Term 内多次投票,破坏选举安全性

**修复**:
```go
// RequestVote RPC 处理器
if args.Term > rf.currentTerm {
    rf.currentTerm = args.Term
    rf.state = Follower
    rf.votedFor = -1
    rf.persist()  // ✅ 必须持久化!
}
```

#### 错误 3: 恢复后未正确初始化易失性状态

**问题**:
```go
rf.readPersist(persister.ReadRaftState())
// ❌ 忘记恢复 commitIndex 和 lastApplied
```

**后果**:
1. `commitIndex` 初始值为 0,但持久化的日志可能已经包含已提交的条目
2. 导致已提交的日志永远不会通过 `applyCh` 发送给上层服务
3. 状态机无法应用到最新状态

**正确做法**:
```go
rf.readPersist(persister.ReadRaftState())
rf.commitIndex = rf.lastIncludedIndex  // 恢复到快照点
rf.lastApplied = rf.lastIncludedIndex
```

### 3C.4 Figure 8 测试解析

**测试场景**: 实现论文 Figure 8 所示的极端场景,验证日志一致性不会破坏安全性。

**挑战**:
1. 领导者可能多次更替
2. 旧领导者提交了来自旧 Term 的日志
3. 新领导者当选并覆盖了这些日志
4. 如果不正确处理,可能导致不同服务器提交不同的日志

**关键修复**: `Commit()` 函数中的**当前 Term 限制**

位置: `raft.go:860`

```go
if newMatchIndex[targetIndex] > rf.commitIndex &&
   rf.getLogTerm(newMatchIndex[targetIndex]) == rf.currentTerm {
    rf.commitIndex = newMatchIndex[targetIndex]
}
```

**原理**: 只有当**候选日志条目的 Term 等于当前领导者的 Term** 时,才能更新 `commitIndex`。这确保了只有当前领导者确认的日志才能被提交。

---

## Part 3D: 日志压缩 (Log Compaction)

### 3D.1 问题背景

随着系统运行,日志会无限增长,导致:
1. **存储压力**: 日志占用大量内存和磁盘
2. **恢复缓慢**: 重启时需要重放完整日志
3. **同步低效**: 新服务器加入时需要同步大量历史日志

**解决方案**: **快照 (Snapshot)**
- 上层服务定期保存状态机的完整状态
- Raft 可以丢弃快照点之前的所有日志
- 只保留快照点之后的日志增量

### 3D.2 实现架构

#### 3D.2.1 日志表示方式

本实现采用 **"虚拟 Dummy Head" + 相对索引** 方案:

```
原始日志 (Raft 论文 1-based):
Index: 1    2    3    4    5    6
Term:  1    1    2    2    3    3
Entry: A    B    C    D    E    F

执行 Snapshot(4) 后:
lastIncludedIndex = 4
lastIncludedTerm = 2

本地日志 (Go slice 0-based):
Slice: [0]   [1]   [2]
Index: 4 →  5     6
Term:  2    3     3
Entry: D    E     F
       ↑
   Dummy Head (代表快照点)
```

**关键设计**:
- `rf.logs[0]` 是虚拟头,不代表真实日志
- `rf.logs[i]` 对应的 Raft 索引 = `rf.lastIncludedIndex + i`
- 所有日志访问都通过 `getFirstLogIndex()` 和 `getLastLogIndex()` 抽象

#### 3D.2.2 快照生成 (`Snapshot()`)

位置: `raft.go:172-225`

**输入参数**:
- `index`: 快照包含的最高日志索引
- `snapshot`: 上层服务提供的序列化状态

**核心逻辑**:

1. **去重检查**:
```go
if index <= rf.lastIncludedIndex {
    return  // 快照已过期,无需处理
}
```

2. **计算相对索引**:
```go
relativeIndex := index - rf.lastIncludedIndex
```

3. **截断日志**:
```go
if relativeIndex < len(rf.logs) {
    // 保留 index 及之后的条目
    rf.lastIncludedTerm = rf.logs[relativeIndex].Term
    newLogs := make([]Entry, 0)
    newLogs = append(newLogs, rf.logs[relativeIndex:]...)
    rf.logs = newLogs
} else {
    // 极端情况:快照比日志还新 (从崩溃恢复时可能发生)
    rf.logs = []Entry{{Term: rf.lastIncludedTerm, Command: nil}}
}
```

4. **更新内部状态**:
```go
rf.logs[0].Term = rf.lastIncludedTerm  // Dummy Head
rf.logs[0].Command = nil

// 确保 commitIndex 和 lastApplied 不落后于快照点
if rf.commitIndex < rf.lastIncludedIndex {
    rf.commitIndex = rf.lastIncludedIndex
}
```

5. **持久化**:
```go
raftState := rf.encodeState()
rf.persister.Save(raftState, snapshot)  // 同时保存 RaftState 和 Snapshot
```

**关键点**: `persister.Save()` 的第二个参数用于保存快照数据,重启后可以通过 `persister.ReadSnapshot()` 恢复。

#### 3D.2.3 快照安装 RPC (`InstallSnapshot`)

当 Follower 的日志落后太多,Leader 已经丢弃了所需的日志时,Leader 会发送 `InstallSnapshot` RPC。

**RPC 定义**:

```go
type InstallSnapshotArgs struct {
    Term               int    // Leader 的 Term
    LeaderId           int    // Leader 的 ID
    LastIncludedIndex  int    // 快照包含的最高日志索引
    LastIncludedTerm   int    // 快照包含的最高日志 Term
    Data               []byte // 快照数据
}
```

**Follower 处理逻辑** (`raft.go:465-540`):

1. **Term 检查**:
```go
if args.Term < rf.currentTerm {
    return  // 拒绝过期的 Leader
}
```

2. **去重优化**:
```go
if args.LastIncludedIndex <= rf.commitIndex {
    return  // 快照已应用,直接丢弃
}
```

**重要**: 这个检查避免了重复应用快照!

3. **日志处理**:

有两种情况:

**情况 A**: 本地日志包含 `args.LastIncludedIndex`
```go
if rf.matchLog(args.LastIncludedIndex, args.LastIncludedTerm) {
    // 保留后缀
    relativeIndex := args.LastIncludedIndex - rf.lastIncludedIndex
    rf.lastIncludedTerm = rf.logs[relativeIndex].Term
    newLogs := make([]Entry, 0)
    newLogs = append(newLogs, rf.logs[relativeIndex:]...)
    rf.logs = newLogs
}
```

**情况 B**: 本地日志不包含该索引 (全部丢弃)
```go
else {
    rf.logs = make([]Entry, 1)
    rf.logs[0].Term = args.LastIncludedTerm
    rf.logs[0].Command = nil  // Dummy Head
}
```

4. **更新状态并持久化**:
```go
rf.logs[0].Command = nil
rf.commitIndex = args.LastIncludedIndex
rf.lastApplied = args.LastIncludedIndex
rf.lastIncludedIndex = args.LastIncludedIndex
rf.lastIncludedTerm = args.LastIncludedTerm

rf.persister.Save(rf.encodeState(), args.Data)
```

5. **发送给上层服务**:
```go
msg := raftapi.ApplyMsg{
    SnapshotValid: true,
    Snapshot:      args.Data,
    SnapshotTerm:  args.LastIncludedTerm,
    SnapshotIndex: args.LastIncludedIndex,
}
rf.applyCh <- msg  // ⚠️ 必须在锁外发送!
```

**关键点**: 发送到 `applyCh` 的操作**必须在锁外完成**,否则可能导致死锁 (上层服务可能在处理消息时调用 Raft API)。

#### 3D.2.4 Leader 发送快照逻辑

位置: `raft.go:865-934` (`BroadcastAppendEntries`)

在 `BroadcastAppendEntries()` 中,Leader 会判断是否需要发送快照:

```go
if rf.nextIndex[server] > rf.getFirstLogIndex() {
    // 正常情况:发送 AppendEntries
    // ...
} else {
    // 快照情况:发送 InstallSnapshot
    args := InstallSnapshotArgs{
        Term:              rf.currentTerm,
        LeaderId:          rf.me,
        LastIncludedIndex: rf.lastIncludedIndex,
        LastIncludedTerm:  rf.lastIncludedTerm,
        Data:              rf.persister.ReadSnapshot(),
    }
    rf.sendInstallSnapshot(server, &args, &reply)
}
```

**判断依据**:
- `nextIndex[server]` 表示 Leader 认为应该发送给 Follower 的下一条日志索引
- 如果 `nextIndex[server] <= firstLogIndex`,说明 Leader 的日志已经被快照丢弃
- 此时必须发送 `InstallSnapshot` RPC

### 3D.3 容易出错的点

#### 错误 1: 快照安装后未更新 `commitIndex`

**问题**:
```go
// InstallSnapshot 处理器
rf.logs = newLogs
rf.lastIncludedIndex = args.LastIncludedIndex
// ❌ 忘记更新 commitIndex 和 lastApplied!
```

**后果**:
1. `commitIndex` 仍然指向旧值 (比如 3)
2. 但 `lastIncludedIndex` 已经更新为 100
3. 导致 `commitIndex < lastIncludedIndex`,违反 Raft 不变量
4. 后续的日志应用逻辑会崩溃 (`rf.logs[commitIndex - lastIncludedIndex]` 会越界)

**修复**:
```go
rf.commitIndex = args.LastIncludedIndex
rf.lastApplied = args.LastIncludedIndex
```

#### 错误 2: 日志索引计算错误

**问题**: Go slice 索引与 Raft 日志索引混淆。

```go
// ❌ 错误:直接使用 Raft 索引访问 slice
entry := rf.logs[index]  // 可能 panic!
```

**正确做法**: 使用辅助函数抽象索引转换

```go
func (rf *Raft) getFirstLogIndex() int {
    return rf.lastIncludedIndex
}

func (rf *Raft) getLastLogIndex() int {
    return rf.lastIncludedIndex + len(rf.logs) - 1
}

func (rf *Raft) getLogTerm(raftIndex int) int {
    firstIndex := rf.getFirstLogIndex()
    if raftIndex == firstIndex {
        return rf.logs[0].Term  // Dummy Head
    }
    sliceIndex := raftIndex - firstIndex
    if sliceIndex < 0 || sliceIndex >= len(rf.logs) {
        return -1  // 越界检查
    }
    return rf.logs[sliceIndex].Term
}
```

**使用示例**:
```go
// ✅ 正确
entry := rf.logs[rf.lastApplied - rf.lastIncludedIndex]
```

#### 错误 3: 在锁内发送 `applyCh` 消息

**问题**:
```go
rf.mu.Lock()
// ... 处理快照 ...
rf.applyCh <- msg  // ❌ 在锁内发送!
rf.mu.Unlock()
```

**后果**: 死锁
1. 上层服务从 `applyCh` 接收消息 (持有服务层锁)
2. 服务处理消息时可能调用 `rf.Start()` (需要获取 `rf.mu`)
3. 但 `rf.mu` 被当前 Goroutine 持有,正在等待服务从 `applyCh` 取消息
4. **循环等待**: 当前 Goroutine 等服务取消息,服务等当前 Goroutine 释放锁

**修复**:
```go
rf.mu.Lock()
// ... 准备 msg ...
rf.mu.Unlock()
rf.applyCh <- msg  // ✅ 在锁外发送
```

#### 错误 4: 快照数据未持久化

**问题**:
```go
rf.persister.Save(rf.encodeState(), nil)  // ❌ 忘记保存快照!
```

**后果**:
1. 服务器崩溃重启后,`persister.ReadSnapshot()` 返回空
2. 但 `lastIncludedIndex` 已经从持久化状态中恢复
3. 日志已经被截断,快照却丢失
4. **数据丢失**: 永久丢失了快照点的所有状态

**修复**:
```go
rf.persister.Save(rf.encodeState(), snapshot)  // ✅ 保存快照
```

#### 错误 5: 重复应用快照

**问题**: 未检查快照是否已应用,直接处理。

**场景**:
1. Leader 发送 `InstallSnapshot` (index=100)
2. Follower 接收并应用快照
3. RPC 回复丢失,Leader 重发
4. Follower 再次应用相同的快照

**后果**:
1. 上层服务状态回滚到旧快照
2. 状态机不一致
3. **数据损坏**

**修复**:
```go
if args.LastIncludedIndex <= rf.commitIndex {
    return  // 快照已应用,直接丢弃
}
```

#### 错误 6: 日志截断时内存泄漏

**问题**:
```go
rf.logs = rf.logs[relativeIndex:]  // ❌ 可能内存泄漏
```

**原因**: Go 的 slice 底层共享数组。如果旧 slice 的前半部分仍被其他引用持有,就无法被 GC 回收。

**修复**: 创建新 slice,确保旧数组可被 GC

```go
newLogs := make([]Entry, 0)
newLogs = append(newLogs, rf.logs[relativeIndex:]...)
rf.logs = newLogs  // ✅ 新数组,旧数组可被 GC
```

### 3D.4 日志一致性优化 (3C 要求)

在 Part 3C 中,要求优化日志回退逻辑,避免 Leader 逐个减 `nextIndex` 重试。

**优化方案**: 在 `AppendEntriesReply` 中返回冲突信息

位置: `raft.go:330-336`

```go
type AppendEntriesReply struct {
    Term              int
    Success           bool
    ConflictLogIndex  int  // 冲突日志的索引
    ConflictLogTerm   int  // 冲突日志的 Term
}
```

**Follower 逻辑** (`raft.go:422-433`):
```go
if args.PrevLogTerm != rf.getLogTerm(args.PrevLogIndex) {
    reply.ConflictLogTerm = rf.getLogTerm(args.PrevLogIndex)
    // 找到 ConflictLogTerm 的第一个索引
    tempIndex := args.PrevLogIndex
    for tempIndex = args.PrevLogIndex - 1; tempIndex > rf.lastIncludedIndex; tempIndex-- {
        if rf.getLogTerm(tempIndex) < reply.ConflictLogTerm {
            break
        }
    }
    reply.ConflictLogIndex = tempIndex + 1
}
```

**Leader 处理逻辑** (`raft.go:812-819`):
```go
if !reply.Success {
    if reply.ConflictLogIndex > 0 {
        if reply.ConflictLogIndex < rf.nextIndex[server] {
            rf.nextIndex[server] = reply.ConflictLogIndex  // 直接跳到冲突点
        }
    } else {
        rf.nextIndex[server] = 1
    }
}
```

**效果**:
- **未优化**: Leader 发送 1000 个 RPC 逐个回退
- **优化后**: Leader 发送 1 个 RPC 直接定位冲突点

---

## 调试经验与技巧

### 1. 使用 `-race` 检测竞态

```bash
go test -race
```

**常见竞态场景**:
- 多个 Goroutine 访问共享变量未加锁
- `applyCh` 发送时持有锁
- RPC 回复处理时的状态检查

### 2. 添加调试日志

**策略**: 只在关键路径添加日志,避免输出爆炸

```go
if rf.lastApplied < rf.lastIncludedIndex {
    fmt.Printf("S%d: lastApplied=%d, lastIncludedIndex=%d\n",
        rf.me, rf.lastApplied, rf.lastIncludedIndex)
}
```

### 3. 可视化工具

测试失败时,会生成可视化时间线文件,帮助理解:
- 网络分区何时发生
- 服务器何时崩溃
- 日志何时提交

### 4. 边界条件测试

手动构造极端场景:
- 所有服务器同时崩溃
- 快照点恰好是日志边界
- 频繁的领导者切换

---

## 性能分析

### RPC 统计 (3D 测试)

| 测试用例 | Peers | RPCs | 时间 |
|---------|-------|------|------|
| snapshots basic | 3 | 232 | 4.9s |
| install snapshots (disconnect) | 3 | 1588 | 60.8s |
| install snapshots (crash) | 3 | 896 | 31.3s |

**观察**:
1. `installSnapshot` 产生大量 RPC (1588 个),说明 Follower 落后较多
2. 快照机制避免了发送数千条日志,显著降低网络开销

### 时间复杂度

- **快照生成**: O(1) - 只是 slice 截断
- **快照安装**: O(N) - N 为快照大小
- **日志恢复**: O(L) - L 为日志长度

---

## 总结与反思

### 技术难点总结

1. **持久化时机**: 何时调用 `persist()` 容易遗漏
2. **索引转换**: Raft 索引 ↔ Go slice 索引易出错
3. **并发控制**: 锁的持有范围直接影响性能和正确性
4. **边界条件**: 快照点、日志首尾、Term 转换等边界情况

### 代码质量优化

1. **辅助函数封装**: `getFirstLogIndex()`, `getLogTerm()` 等
2. **状态机不变量**: 明确约束,如 `commitIndex >= lastIncludedIndex`
3. **防御性编程**: 检查输入合法性,避免 Panic

### Raft 协议理解

通过实现 3C 和 3D,深刻理解了:
- **状态持久化** 是分布式系统容错的基石
- **快照机制** 在保持一致性的同时优化存储和恢复
- **安全性** (Safety) 比 **活性** (Liveness) 更关键

### 后续改进方向

1. **增量快照**: 避免每次全量序列化
2. **流水线优化**: 并行发送多个 RPC
3. **批处理**: 合并多个日志条目到一个 RPC

---

## 参考资料

1. [Diego Ongaro, "Log: A New Foundation for Distributed Systems"](https://github.com/ongardie/dissertation)
2. [MIT 6.5840 Lab 3 说明文档](https://cdn.jsdelivr.net/gh/mit-6-5840/labs/2025/raft.html)
3. [Raft Extended Paper, Figure 2 & Section 7](https://github.com/ongardie/dissertation)

---

**完成日期**: 2026-01-06
**作者**: Neil
**课程**: MIT 6.5840 Distributed Systems
