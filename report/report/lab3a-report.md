# 6.5840 Lab 3A: Raft Leader Election 实验报告

## 一、实验概述

### 1.1 实验目标
本实验是构建容错键值存储系统系列实验的第一部分，要求实现Raft共识算法的**领导者选举**和**心跳机制**。Raft是一种分布式一致性算法，通过将复杂的共识问题分解为相对独立的子问题（领导者选举、日志复制、安全性等），使得算法易于理解和实现。

### 1.2 核心挑战
- **领导者选举**: 在网络分区、节点故障等情况下快速选出唯一的领导者
- **心跳机制**: 领导者通过心跳维持权威，防止不必要的选举
- **安全性保证**: 确保同一任期最多只有一个领导者被选出
- **选举超时**: 随机化超时机制避免投票分裂
- **并发控制**: 多goroutine并发执行时的状态同步和互斥保护

### 1.3 技术栈
- **编程语言**: Go
- **并发控制**: sync.Mutex, sync/atomic
- **通信机制**: RPC (labrpc)
- **定时机制**: time.Sleep, time.Timer
- **随机化**: math/rand

### 1.4 实验环境
```
┌─────────────┐    RPC Network    ┌─────────────┐
│   Server 0  │◄─────────────────►│   Server 1  │
│   (Peer)    │                   │   (Peer)    │
├─────────────┤                   ├─────────────┤
│   Server 2  │◄─────────────────►│   Server 3  │
│   (Peer)    │                   │   (Peer)    │
└─────────────┘                   └─────────────┘
         ▲                               ▲
         │                               │
    任一节点可能成为Leader              测试器通过RPC调用
```

## 二、系统设计与架构

### 2.1 Raft算法概述

Raft将共识问题分解为三个相对独立的子问题：
1. **领导者选举** (Leader Election) - 本次实验实现
2. **日志复制** (Log Replication) - Lab 3B
3. **安全性** (Safety) - Lab 3B/3C
4. **持久化** (Persistence) - Lab 3C
5. **日志压缩** (Log Compaction) - Lab 3D

### 2.2 服务器状态机

Raft服务器有三种状态：

```
     ┌─────────┐  选举超时   ┌───────────┐
     │ Follower│──────────► │ Candidate │
     └─────────┘            └───────────┘
         ▲                      │
         │ 收到心跳           获得多数票
         │                      │
         │                      ▼
         │                   ┌─────────┐
         └───────────────────│ Leader  │
          收到更高Term的RPC    └─────────┘
```

**状态转换规则**:
- **Follower → Candidate**: 选举超时，未收到领导者的心跳
- **Candidate → Leader**: 获得多数服务器的投票
- **Candidate/Leader → Follower**: 收到更高Term的RPC或发现自己的Term过期
- **任意状态 → Follower**: 发现更高Term的存在

### 2.3 核心数据结构

#### 2.3.1 Raft结构体
```go
type Raft struct {
    mu        sync.Mutex          // 互斥锁保护共享状态
    peers     []*labrpc.ClientEnd // 所有对等节点的RPC端点
    persister *tester.Persister   // 持久化对象
    me        int                 // 此节点在peers[]中的索引
    dead      int32               // 由Kill()设置

    // 3A: 领导者选举相关状态
    currentTerm int                // 最新任期号（首次启动为0，单调递增）
    votedFor    int                // 当前任期投票给的候选者ID（-1表示未投票）
    logs        []Entry            // 日志数组（日志条目按索引顺序存储）
    state       RaftState          // 当前状态（Leader/Follower/Candidate）
    heartbeatCh chan bool          // 心跳通道，用于重置选举定时器
}
```

**设计要点**:
- **currentTerm**: 任期号是Raft中的逻辑时间概念，每次选举后递增
- **votedFor**: 确保每个节点在每个任期最多投票一次，防止投票分裂
- **heartbeatCh**: 缓冲通道，用于通知ticker重置选举超时
- **logs**: 为后续日志复制预留，当前版本为空或包含虚拟初始条目

#### 2.3.2 服务器状态枚举
```go
type RaftState = int
const (
    Leader    RaftState = iota  // 0: 领导者
    Follower                    // 1: 跟随者
    Candidate                   // 2: 候选者
)
```

#### 2.3.3 日志条目结构
```go
type Entry struct {
    Term    int   // 日志条目的任期号
    Command any   // 状态机命令（3B中使用）
}
```

### 2.4 RPC消息结构

#### 2.4.1 RequestVote RPC
**请求参数** (`RequestVoteArgs`):
```go
type RequestVoteArgs struct {
    Term         int  // 候选者的任期号
    CandidateId  int  // 候选者的节点ID
    LastLogIndex int  // 候选者最后一条日志条目的索引
    LastLogTerm  int  // 候选者最后一条日志条目的任期号
}
```

**响应参数** (`RequestVoteReply`):
```go
type RequestVoteReply struct {
    Term        int   // 当前任期号，用于候选者更新自己的任期
    VoteGranted bool  // true表示候选者获得投票
}
```

#### 2.4.2 AppendEntries RPC (心跳)
**请求参数** (`AppendEntriesArgs`):
```go
type AppendEntriesArgs struct {
    Term         int     // 领导者的任期号
    LeaderId     int     // 领导者的节点ID
    PrevLogIndex int     // 前一个日志条目的索引（3B使用）
    PrevLogTerm  int     // 前一个日志条目的任期号（3B使用）
    LeaderCommit int     // 领导者的已提交索引（3B使用）
    Entries      []Entry // 准备追加的日志条目（心跳时为空）
}
```

**响应参数** (`AppendEntriesReply`):
```go
type AppendEntriesReply struct {
    Term    int   // 当前任期号，用于领导者更新自己的任期
    Success bool  // 跟随者包含匹配PrevLogIndex和PrevLogTerm的条目
}
```

## 三、核心算法实现

### 3.1 领导者选举算法

#### 3.1.1 选举触发机制
选举由ticker goroutine在选举超时后自动触发：

```go
func (rf *Raft) ticker() {
    for rf.killed() == false {
        // 1. 生成随机超时时间（300-600ms）
        timeout := rf.getRandomTimeout()

        // 2. 创建定时器
        timer := time.NewTimer(timeout)

        select {
        case <-timer.C:
            // 超时：发起选举（仅非Leader节点）
            rf.mu.Lock()
            state := rf.state
            rf.mu.Unlock()

            if state != Leader {
                rf.StartElection()
            }

        case <-rf.heartbeatCh:
            // 收到心跳：重置定时器
            if !timer.Stop() {
                select {
                case <-timer.C:
                default:
                }
            }
        }
    }
}
```

**随机超时机制**:
```go
func (rf *Raft) getRandomTimeout() time.Duration {
    ms := 300 + (rand.Int63() % 300) // 300-600ms
    return time.Duration(ms) * time.Millisecond
}
```

**设计要点**:
- **随机化**: 避免多个节点同时超时导致投票分裂
- **超时范围**: 300-600ms，满足测试要求的5秒内完成选举
- **心跳响应**: 收到心跳立即重置定时器，无需等待超时

#### 3.1.2 选举发起流程
```go
func (rf *Raft) StartElection() {
    rf.mu.Lock()

    // 1. 转换为候选者状态
    rf.state = Candidate
    rf.currentTerm = rf.currentTerm + 1
    rf.votedFor = rf.me  // 自己投自己一票

    currentTerm := rf.currentTerm
    votes := 1  // 已获得自己的一票

    // 2. 准备RequestVote参数
    args := RequestVoteArgs{
        Term:         currentTerm,
        CandidateId:  rf.me,
        LastLogIndex: 0,
        LastLogTerm:  0,
    }

    rf.mu.Unlock()

    // 3. 并发发送RequestVote RPC
    for i := range rf.peers {
        if i == rf.me { continue }

        go func(server int) {
            reply := RequestVoteReply{}
            if rf.sendRequestVote(server, &args, &reply) {
                rf.handleVoteReply(server, args, reply, &votes)
            }
        }(i)
    }
}
```

而此处的 handleVoteReply 函数也颇有讲究：

```go
    for i := range rf.peers {
		if i == rf.me { continue }
		// 构造 RequestVoteArgs (深拷贝)
        // 启动 Goroutine 发送 (核心：不阻塞 ticker)
        go func(server int) {
			reply := RequestVoteReply{}
			// fmt.Println("send vote to dst = ", server)
            if rf.sendRequestVote(server, &args, &reply) {
				// maxTerm 需要更新为 Vote reply 里最大的 Term
				// votes 需要更新回复 VoteGranted 的 server 数量
				rf.mu.Lock()
				

				if rf.state != Candidate || rf.currentTerm != args.Term {
					rf.mu.Unlock()
					return
				}

				if reply.Term > rf.currentTerm {
					rf.state = Follower
					rf.votedFor = -1
					rf.mu.Unlock()
					return
				}
				
				if reply.VoteGranted {
					votes++
					// fmt.Println("votes = ", votes)
					if votes > len(rf.peers) / 2{
						rf.state = Leader
						rf.mu.Unlock()
						rf.BroadcastHeartbeat()
						return
					}
				}

				rf.mu.Unlock()
            }
        }(i)
	}

```
因为涉及到 rf.state 的更改，所以需要申请锁，但是针对不同的情况，锁释放的时机是不同的。如果统一用 defer rf.mu.Unlock()，那会导致当 server 当选后，使用 BroadcastHeartbeat 函数进行广播的时候，导致死锁。


**关键设计**:
- **Term递增**: 每次选举Term必须递增，确保新任期的权威性
- **自投票**: 候选者首先投票给自己，简化投票计数逻辑
- **并发RPC**: 使用goroutine并发发送投票请求，提高选举速度
- **参数深拷贝**: 避免并发访问时的数据竞争

#### 3.1.3 投票请求处理
```go
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    reply.VoteGranted = false

    // 1. 任期检查
    if args.Term < rf.currentTerm {
        reply.Term = rf.currentTerm
        return  // 拒绝过期任期的投票请求
    }

    // 2. 更新到新任期
    if args.Term > rf.currentTerm {
        rf.votedFor = -1  // 清除旧任期的投票记录
    }

    // 3. 检查是否已投票
    if rf.votedFor != args.CandidateId && rf.votedFor != -1 {
        reply.Term = rf.currentTerm
        return  // 已经投给别人了
    }

    // 4. 日志完整性检查（Raft论文5.4.1节）
    lastLogIndex := len(rf.logs) - 1
    coldStartFlag := (lastLogIndex < 0)

    lastLogTerm := rf.logs[lastLogIndex].Term
    voteFlag := lastLogTerm < args.LastLogTerm ||
                (lastLogTerm == args.LastLogTerm && lastLogIndex <= args.LastLogIndex)


    // 5. 授予投票
    if voteFlag {
        reply.VoteGranted = true
        rf.votedFor = args.CandidateId
        rf.currentTerm = args.Term
        rf.state = Follower

        // 【关键】投票后重置自己的选举定时器
        select {
        case rf.heartbeatCh <- true:
        default:
        }
    }

    reply.Term = rf.currentTerm
}
```

此处容易忽略的是，在发现自己的 term 比别的 server term 更小时，需要清空 votedFor。这会导致在第二次选举时，部分 server 只给特定的 server 投票，导致一轮投票中远达不到 server 数量，最后导致不停地 split vote。

**易错点补充**:

1. **Dummy Head设计**:
在3B实现中，每个节点的`logs`数组都会有一个dummy head（index=0），用于简化索引计算:
```go
// 初始化时添加虚拟头部
rf.logs = []Entry{{Term: 0, Command: nil}}

// 这样实际日志从index=1开始，避免了0和-1下标的边界讨论
// len(logs) - 1 就是最后一条日志的索引
```

这个设计的优势:
- 简化索引计算，避免边界检查
- `PrevLogIndex=0`是有效的，指向dummy head
- 日志索引直接映射到数组下标

2. **voteFlag的逻辑错误**:
投票判断条件容易写错:
```go
// ❌ 错误: 逻辑颠倒
voteFlag := lastLogTerm > args.LastLogTerm ||
           (lastLogTerm == args.LastLogTerm && lastLogIndex >= args.LastLogIndex)

// ✅ 正确: 应该给比自己"新或一样新"的候选者投票
voteFlag := lastLogTerm < args.LastLogTerm ||
           (lastLogTerm == args.LastLogTerm && lastLogIndex <= args.LastLogIndex)
```

**记忆技巧**: 如果候选者的日志"至少和我一样新"，就投票给他。即候选者的日志索引应该**大于等于**我的日志索引。

**已修复的关键Bug**:
```go
// 在 RequestVote RPC 中添加的关键修复
if args.Term > rf.currentTerm {
    rf.votedFor = -1  // 清除旧任期的投票记录！
}
```

这个修复确保当节点收到更高任期的投票请求时，会清除之前的投票记录，从而避免：
1. 连续选举时的投票锁定问题
2. 节点因保留旧任期投票记录而无法参与新任期选举
3. 导致投票分裂和选举超时

**日志完整性检查**:
根据Raft论文5.4.1节，投票限制规则为：
- 如果候选者的日志至少和自己一样新（newer），则授予投票
- 比较最后日志条目的Term和Index：
  - Term更大 → 更新
  - Term相同但Index更大 → 更新
  - 否则拒绝

**关键修复**:
```go
// 既然投了票，就承认了对方的地位，必须重置自己的选举定时器！
// 否则刚投完票，马上自己超时，又去拆台。
select {
case rf.heartbeatCh <- true:
default:
}
```
这个修复确保投票后立即重置定时器，避免投票后马上发起竞争选举。

#### 3.1.4 投票响应处理
```go
func (rf *Raft) handleVoteReply(server int, args RequestVoteArgs,
                                  reply RequestVoteReply, votes *int) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    // 1. 状态检查：如果不是候选者或任期已变，忽略响应
    if rf.state != Candidate || rf.currentTerm != args.Term {
        return
    }

    // 2. 任期更新：发现更高任期
    if reply.Term > rf.currentTerm {
        rf.state = Follower
        rf.votedFor = -1
        rf.currentTerm = reply.Term
        return
    }

    // 3. 计票：获得投票
    if reply.VoteGranted {
        *votes++
        if *votes > len(rf.peers) / 2 {
            // 获得多数投票，成为领导者
            rf.state = Leader
            rf.BroadcastHeartbeat()
        }
    }
}
```

**选举完成条件**:
- 获得超过半数节点的投票（`votes > len(peers) / 2`）
- 立即开始发送心跳，巩固领导地位

### 3.2 心跳机制

#### 3.2.1 心跳发送循环
```go
func (rf *Raft) heartbeatTicker() {
    const heartbeatInterval = 100 * time.Millisecond  // 心跳间隔

    for rf.killed() == false {
        rf.mu.Lock()
        state := rf.state
        rf.mu.Unlock()

        if state == Leader {
            rf.BroadcastHeartbeat()
        }

        time.Sleep(heartbeatInterval)
    }
}
```

**设计要点**:
- **心跳频率**: 每100ms发送一次，满足测试要求（不超过10次/秒）
- **仅Leader发送**: 避免非Leader节点发送无效心跳
- **固定间隔**: 确保跟随者不会超时

#### 3.2.2 广播心跳
```go
func (rf *Raft) BroadcastHeartbeat() {
    rf.mu.Lock()
    if rf.state != Leader {
        rf.mu.Unlock()
        return
    }

    currentTerm := rf.currentTerm
    rf.mu.Unlock()  // 发送RPC前释放锁

    for i := range rf.peers {
        if i == rf.me { continue }

        args := AppendEntriesArgs{
            Term:     currentTerm,
            LeaderId: rf.me,
            Entries:  []Entry{},  // 心跳不携带日志条目
        }

        go func(server int, args AppendEntriesArgs) {
            reply := AppendEntriesReply{}
            if rf.sendAppendEntries(server, &args, &reply) {
                rf.handleAppendEntriesReply(server, args, reply)
            }
        }(i, args)
    }
}
```

**心跳特点**:
- **空日志**: Entries为空数组，仅用于维持权威
- **并发发送**: 使用goroutine并发发送，提高效率
- **包含Term**: 跟随者可借此更新自己的Term

#### 3.2.3 心跳接收处理
```go
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    // 1. 初始化
    reply.Term = rf.currentTerm
    reply.Success = false

    // 2. 任期检查
    if args.Term < rf.currentTerm {
        return  // 拒绝过期任期的心跳
    }

    // 3. 接受来自领导者或更高任期的心跳
    if args.Term >= rf.currentTerm {
        rf.currentTerm = args.Term
        rf.state = Follower

        // 重置选举定时器
        select {
        case rf.heartbeatCh <- true:
        default:
        }
    }

    reply.Success = true
}
```

**心跳作用**:
1. **权威确认**: 跟随者确认领导者的存在
2. **Term同步**: 更新到最新任期
3. **重置定时器**: 防止不必要的选举
4. **状态转换**: 候选者收到心跳后转为跟随者

#### 3.2.4 心跳响应处理
```go
func (rf *Raft) handleAppendEntriesReply(server int, args AppendEntriesArgs,
                                          reply AppendEntriesReply) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    // 发现更高任期，转为跟随者
    if reply.Term > rf.currentTerm {
        rf.currentTerm = reply.Term
        rf.state = Follower
        rf.votedFor = -1  // 清除投票记录
    }
}
```

**响应处理逻辑**:
- Leader发现更高Term时主动退位
- 清除投票记录，允许参与下一轮选举
- 确保系统中最多只有一个Leader

### 3.3 状态查询接口

```go
func (rf *Raft) GetState() (int, bool) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    term := rf.currentTerm
    isLeader := (rf.state == Leader)

    return term, isLeader
}
```

注意此处虽然只是读一个 state 字段，但是如果不加上并发锁，最后在 -race 的检查下，会出现 data race 的情况；

**接口说明**:
- 返回当前任期号
- 返回是否为领导者
- 用于测试和上层服务判断Leader身份

## 四、关键技术与挑战

### 4.1 并发控制与同步

#### 4.1.1 互斥锁保护策略
```go
rf.mu.Lock()
defer rf.mu.Unlock()

// 在锁保护下访问和修改共享状态：
// - currentTerm
// - votedFor
// - state
// - logs
```

**锁的使用原则**:
1. **细粒度锁**: 持锁时间尽可能短
2. **避免死锁**: 不在持锁时发送RPC或进行耗时操作
3. **一致性**: 所有共享状态的访问都在锁保护下

#### 4.1.2 并发RPC发送
```go
// 错误示例：在持锁时发送RPC
rf.mu.Lock()
for i := range rf.peers {
    rf.sendRequestVote(i, &args, &reply)  // 阻塞调用！
}
rf.mu.Unlock()

// 正确示例：在锁外并发发送
rf.mu.Lock()
args := prepareArgs()
rf.mu.Unlock()

for i := range rf.peers {
    go func(server int) {
        rf.sendRequestVote(server, &args, &reply)
    }(i)
}
```

**并发设计要点**:
- **深拷贝参数**: 避免并发访问时的数据竞争
- **Goroutine隔离**: 每个RPC在独立的goroutine中执行
- **状态验证**: RPC响应处理时验证状态未变

#### 4.1.3 通道同步机制
```go
// 心跳通道（缓冲1）
heartbeatCh chan bool

// 发送心跳（非阻塞）
select {
case rf.heartbeatCh <- true:
default:
}

// 接收心跳（非阻塞）
select {
case <-rf.heartbeatCh:
    // 重置定时器
default:
    // 无心跳，继续等待
}
```

**通道设计优势**:
- **非阻塞**: 不阻塞ticker和心跳处理
- **防丢失**: 缓冲大小为1，确保信号不丢失
- **解耦**: 心跳发送者和接收者解耦

### 4.2 安全性保证

#### 4.2.1 唯一Leader保证
Raft通过以下机制确保同一任期最多一个Leader：

1. **任期机制**:
   - 每次选举Term递增
   - 服务器拒绝过期Term的请求
   - 发现更高Term时主动退位

2. **投票限制**:
   - 每个节点每任期最多投一次票
   - 日志完整性检查确保最新日志的候选者获胜
   - `votedFor`字段持久化，防止重启后重复投票

3. **多数派原则**:
   - 必须获得超过半数投票才能成为Leader
   - 两个候选者不可能同时获得多数票（证明见Raft论文）

#### 4.2.2 选举安全性证明
**定理**: Raft保证在任何任期中最多选举出一个Leader。

**证明**:
1. 假设某个任期T有两个候选者同时获胜
2. 这意味着至少有一个候选者获得了多数票
3. 在多数票中必然存在某个节点N同时投给了两个候选者
4. 根据投票规则，节点N在同一任期只能投一次票
5. 矛盾！因此假设不成立

#### 4.2.3 日志完整性检查
```go
lastLogTerm := rf.logs[lastLogIndex].Term
voteFlag = lastLogTerm > args.LastLogTerm ||
           (lastLogTerm == args.LastLogTerm && lastLogIndex >= args.LastLogIndex)
```

**检查目的**:
- 确保投票给日志至少和自己一样新的候选者
- 防止日志不完整的候选者当选Leader
- 为日志复制阶段的安全性奠定基础

### 4.3 网络容错处理

#### 4.3.1 RPC超时和重试
```go
// labrpc的Call()方法内置超时机制
ok := rf.peers[server].Call("Raft.RequestVote", args, reply)

// 返回false的可能原因：
// 1. 目标服务器宕机
// 2. 网络分区
// 3. 请求丢失
// 4. 响应丢失
```

**容错机制**:
- **自动超时**: labrpc层处理，无需应用层关心
- **忽略失败**: 选举过程中RPC失败不影响正确性
- **心跳保持**: Leader定期发送心跳，容忍短暂的网络故障

#### 4.3.2 网络分区处理
**场景1**: Leader被分区隔离
- Leader无法获得多数响应，最终退位
- 分区内的节点发起选举，选出新Leader
- 网络恢复后，旧Leader发现更高Term，转为Follower

**场景2**: Follower被分区隔离
- Follower收不到心跳，选举超时
- 发起选举，但无法获得多数票（被分区）
- 持续重试直到网络恢复

### 4.4 性能优化

#### 4.4.1 随机超时调优
```go
func (rf *Raft) getRandomTimeout() time.Duration {
    ms := 300 + (rand.Int63() % 300) // 300-600ms
    return time.Duration(ms) * time.Millisecond
}
```

**参数选择理由**:
- **最小值300ms**: 确保心跳能及时到达（心跳间隔100ms）
- **随机范围300ms**: 足够大的随机窗口降低冲突概率
- **最大值600ms**: 满足5秒内完成选举的测试要求

**理论分析**:
- 3个节点的冲突概率：约33%
- 5个节点的冲突概率：更低
- 实际测试中，大多数情况下1-2轮选举即可成功

#### 4.4.2 并发RPC优化
```go
// 并发发送，而不是串行
for i := range rf.peers {
    go func(server int) {
        rf.sendRequestVote(server, &args, &reply)
    }(i)
}
```

**性能优势**:
- **降低延迟**: 不等待慢速节点
- **提高吞吐**: 充分利用网络带宽
- **快速收敛**: 更快获得多数投票

#### 4.4.3 心跳频率优化
```go
const heartbeatInterval = 100 * time.Millisecond
```

**设计权衡**:
- **高频心跳** (100ms): 快速检测Leader故障
- **网络开销**: 每秒10个RPC，满足测试限制
- **CPU开销**: Sleep机制，避免忙等待

## 五、测试与验证

### 5.1 测试用例分析

#### 5.1.1 初始选举测试
```bash
Test (3A): initial election (reliable network)...
  ... Passed --   3.6  3   106    0
```

**测试内容**:
- 启动3个Raft节点
- 验证能在初始状态下选出一个Leader
- 检查GetState()返回正确

**关键验证点**:
- 只有一个节点认为自己是Leader
- Leader的Term应该是1（从0递增到1）
- 其他节点是Follower，Term也是1

#### 5.1.2 重新选举测试
```bash
Test (3A): election after network failure (reliable network)...
  ... Passed --   7.6  3   304    0
```

**测试内容**:
- 选出Leader后，将其禁用（网络分区）
- 验证剩余节点能在5秒内选出新Leader
- 恢复原Leader，验证其转为Follower

**关键验证点**:
- 旧Leader无法维持权威后，新Leader被选出
- 旧Leader恢复后正确识别新Leader的权威
- Term正确递增

#### 5.1.3 多轮选举测试
```bash
Test (3A): multiple elections (reliable network)...
  ... Passed --   8.4  7   954    0
```

**测试内容**:
- 进行多次Leader失效和恢复
- 验证每次都能正确选举出新Leader
- 测试系统在长时间运行下的稳定性

**关键验证点**:
- 每次选举Term都递增
- 不会出现两个同时存在的Leader
- 所有节点最终收敛到同一状态

### 5.2 性能指标分析

#### 5.2.1 测试输出解读
```bash
... Passed --   3.6  3   106    0
│             │    │   │    │
│             │    │   │    └─ 提交的日志条目数（3A为0）
│             │    │   └─────── RPC发送总字节数
│             │    └─────────── RPC总次数
│             └──────────────── 节点数量
└─────────────────────────── 测试耗时（秒）
```

#### 5.2.2 性能评估
- **选举延迟**: 通常1-2个超时周期（300-1200ms）
- **RPC开销**: 每次选举约30-40个RPC（3节点）
- **网络带宽**: 每个RPC约几十字节，开销很小
- **CPU使用**: Sleep机制，CPU使用率低

### 5.3 正确性验证

#### 5.3.1 唯一Leader检查
```go
// 测试代码检查逻辑
func TestInitialElection3A(t *testing.T) {
    // 启动服务器
    servers := startup(t, 3)

    // 检查Leader唯一性
    leaderCount := 0
    for i := 0; i < 3; i++ {
        _, isLeader := servers[i].GetState()
        if isLeader {
            leaderCount++
        }
    }

    if leaderCount != 1 {
        t.Fatalf("expected one leader, got %d", leaderCount)
    }
}
```

#### 5.3.2 Term一致性检查
```go
// 所有节点的Term应该一致
term0, _ := servers[0].GetState()
term1, _ := servers[1].GetState()
term2, _ := servers[2].GetState()

if term0 != term1 || term1 != term2 {
    t.Fatalf("terms do not match: %d %d %d", term0, term1, term2)
}
```

### 5.4 边界情况测试

#### 5.4.1 冷启动
- **场景**: 所有节点同时启动
- **验证**: 能够正确选出一个Leader
- **处理**: 随机超时避免同时发起选举

#### 5.4.2 网络分区
- **场景**: Leader被隔离
- **验证**: 剩余节点选出新Leader
- **处理**: 旧Leader超时后转为Follower

#### 5.4.3 节点重启
- **场景**: 节点Kill后重启
- **验证**: 能够参与后续选举
- **处理**: 重启后状态恢复，votedFor正确初始化

## 六、实验总结与反思

### 6.1 实现特点

1. **清晰的状态机**: 三种状态清晰定义，转换逻辑明确
2. **并发安全**: 使用互斥锁保护所有共享状态，避免数据竞争
3. **高效通信**: 并发RPC发送，心跳机制简洁有效
4. **容错能力**: 能够处理节点故障、网络分区等异常情况
5. **可扩展性**: 为后续日志复制、持久化等功能预留接口

### 6.2 关键技术收获

#### 6.2.1 分布式共识算法
- **Raft设计理念**: 将复杂问题分解为简单子问题
- **领导者选举**: 理解了选举超时、投票机制、任期管理的原理
- **安全性保证**: 掌握了唯一Leader、投票限制等关键机制

#### 6.2.2 并发编程技巧
- **互斥锁使用**: 学会了何时加锁、持锁时间控制
- **Goroutine管理**: 掌握了并发RPC的正确模式
- **通道通信**: 理解了非阻塞通道在定时器重置中的作用

#### 6.2.3 系统设计能力
- **状态机设计**: 三种状态及其转换逻辑
- **RPC协议**: 设计了清晰的消息结构和处理流程
- **容错设计**: 超时、重试、状态恢复等机制

### 6.3 遇到的挑战与解决方案

#### 6.3.1 挑战1: 投票后立即发起竞争选举
**问题**: 节点投票后，自己的选举定时器未重置，马上又发起选举，导致投票分裂

**解决方案**:
```go
// RequestVote RPC处理中
if voteFlag {
    reply.VoteGranted = true
    rf.votedFor = args.CandidateId
    rf.state = Follower

    // 投票后立即重置自己的选举定时器
    select {
    case rf.heartbeatCh <- true:
    default:
    }
}
```

**启示**: 投票不仅是给予对方权力，也意味着承认对方的权威，自己应该重置状态。

#### 6.3.2 挑战2: RPC响应处理时的状态一致性
**问题**: RPC返回时，节点的状态可能已经改变（如Term变化、角色变化）

**解决方案**:
```go
func (rf *Raft) handleVoteReply(server int, args RequestVoteArgs,
                                  reply RequestVoteReply, votes *int) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    // 状态检查：忽略过期响应
    if rf.state != Candidate || rf.currentTerm != args.Term {
        return
    }

    // 处理响应...
}
```

**启示**: 分布式系统中必须时刻考虑消息延迟和状态变化，响应处理时需要验证状态一致性。

#### 6.3.3 挑战3: 并发RPC时的数据竞争
**问题**: 多个goroutine并发发送RPC，访问共享状态可能导致数据竞争

**解决方案**:
```go
// 在持锁时准备参数（深拷贝）
rf.mu.Lock()
args := RequestVoteArgs{
    Term:        rf.currentTerm,
    CandidateId: rf.me,
    // ...
}
rf.mu.Unlock()

// 在锁外并发发送RPC
for i := range rf.peers {
    go func(server int) {
        rf.sendRequestVote(server, &args, &reply)
    }(i)
}
```

**启示**: 并发编程中要明确区分"需要同步的临界区"和"可以并行的操作"。

### 6.4 设计权衡分析

#### 6.4.1 超时范围选择
**权衡因素**:
- **过小** (如150-300ms): 增加投票冲突概率，导致多次重选
- **过大** (如1000-2000ms): 无法满足5秒内完成选举的测试要求
- **选择** (300-600ms): 在冲突概率和响应速度间取得平衡

#### 6.4.2 心跳频率选择
**权衡因素**:
- **高频** (如10ms): 快速检测故障，但增加网络和CPU开销
- **低频** (如500ms): 降低开销，但故障检测延迟高
- **选择** (100ms): 满足测试限制（≤10次/秒），故障检测延迟可接受

#### 6.4.3 并发控制粒度
**权衡因素**:
- **细粒度锁**: 提高并发度，但增加实现复杂度和死锁风险
- **粗粒度锁**: 简单可靠，但可能限制性能
- **选择**: 单一互斥锁保护所有状态，简化实现，3A阶段性能足够

### 6.5 局限性与改进方向

#### 6.5.1 当前局限
1. **无持久化**: 节点重启后状态丢失（Lab 3C解决）
2. **无日志复制**: 仅实现了选举，未实现状态机命令复制（Lab 3B解决）
3. **单线程处理**: ticker和heartbeatTicker各自独立，可能有优化空间
4. **调试困难**: 缺乏详细的日志和可视化工具

#### 6.5.2 改进建议
1. **添加调试日志**: 记录状态转换、RPC发送等关键事件
2. **性能监控**: 统计RPC次数、选举耗时等指标
3. **可视化工具**: 实现状态机的可视化展示
4. **单元测试**: 为关键函数添加单元测试，提高代码质量

### 6.6 实验心得

通过实现Raft的领导者选举机制，我深入理解了：

1. **分布式系统的复杂性**: 即使看似简单的"选领导"问题，也需要仔细考虑网络分区、消息延迟、并发访问等各种边界情况
2. **状态机的价值**: 清晰定义状态和转换规则，使得算法逻辑易于理解和验证
3. **并发编程的挑战**: 数据竞争、死锁、消息延迟等问题需要仔细设计和大量测试
4. **Raft的设计智慧**: 通过任期、投票限制、多数派原则等简单机制实现复杂的分布式共识
5. **测试的重要性**: 分布式系统的正确性依赖于完善的测试用例和形式化验证

这个实验为后续实现日志复制、持久化等更复杂的功能奠定了坚实的基础。我深刻体会到，**简单清晰的代码比巧妙复杂的代码更有价值**，特别是在分布式系统中。Raft的设计哲学——将复杂问题分解为简单子问题——不仅体现在算法设计上，也应该体现在代码实现中。

## 七、参考文献

1. **Diego Ongaro and John Ousterhout. "In Search of an Understandable Consensus Algorithm (Raft)."** USENIX Annual Technical Conference, 2014.
   - Raft算法的原始论文，详细阐述了领导者选举、日志复制等机制

2. **Raft Extended Paper.** https://github.com/ongardie/dissertation/blob/master/raft-extended.pdf
   - Raft扩展论文，包含日志压缩、集群成员变更等高级主题

3. **MIT 6.5840 Course Materials.** Spring 2025.
   - 课程实验说明、测试代码和评分标准

4. **Go Documentation.** https://golang.org/doc/
   - Go语言官方文档，包括并发编程、RPC等主题

5. **Tony Bai. "Raft Algorithm Visualization."** https://github.com/ongardie/raft.github.io/
   - Raft算法可视化工具，帮助理解算法执行过程
