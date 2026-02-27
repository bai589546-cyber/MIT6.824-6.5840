# Lab 5A: ShardKV 配置管理与分片迁移

## 实验概述

Lab 5A 实现了 ShardKV 的配置管理和分片迁移机制。ShardKV 将数据按 key 分片（shard）分布到不同的 shard group 中，每个 shard group 由多个副本组成，使用 Raft 保证一致性。当配置发生变化时（如加入/删除 group），需要将 shard 从源 group 迁移到目标 group。

### 核心组件与层次结构

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                              ShardKV 系统架构                                    │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │                        ShardKV Client (应用层)                           │   │
│  │  Put(key, value, ver)  /  Get(key)                                      │   │
│  └───────────────────────────────────┬─────────────────────────────────────┘   │
│                                      │                                         │
│                                      │ 1. Query() 获取配置                     │
│                                      ▼                                         │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │                    ShardCtrler (配置管理层)                               │   │
│  │  ┌──────────────────────────────────────────────────────────────────┐   │   │
│  │  │  配置存储 (使用 KVRaft 实现)                                       │   │   │
│  │  │  - "config-N": 配置号 N 的完整配置                                  │   │   │
│  │  │  - "latest": 当前最新配置号                                         │   │   │
│  │  │                                                                  │   │   │
│  │  │  ShardConfig 结构:                                                │   │   │
│  │  │  - Num: 配置号                                                    │   │   │
│  │  │  - Shards[10]: [1,1,1,2,2,2,3,3,3,3]  (每个 shard 属于哪个 GID)   │   │   │
│  │  │  - Groups: {1: [s1,s2,s3], 2: [s4,s5,s6], 3: [s7,s8,s9]}         │   │   │
│  │  └──────────────────────────────────────────────────────────────────┘   │   │
│  └───────────────────────────────────┬─────────────────────────────────────┘   │
│                                      │                                         │
│                                      │ 2. 根据 key 计算 shard → GID             │
│                                      │    shard = Key2Shard(key) = hash(key) % 10 │
│                                      │    gid = Shards[shard]                   │
│                                      ▼                                         │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │                    ShardGrp Client (路由层)                               │   │
│  │  根据 GID 选择目标 Shard Group，转发请求                                  │   │
│  └───────────────────────────────────┬─────────────────────────────────────┘   │
│                                      │                                         │
│                                      ▼                                         │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │                    Shard Group (数据存储层)                               │   │
│  │  每个 ShardGroup 本质上就是一个独立的 KVRaft 服务                         │   │
│  │                                                                          │   │
│  │  ┌────────────────────────────────────────────────────────────────┐     │   │
│  │  │  ShardGroup GID=1  (拥有 shard 0,1,2)                           │     │   │
│  │  │  ┌────────────┐  ┌────────────┐  ┌────────────┐               │     │   │
│  │  │  │   Server   │  │   Server   │  │   Server   │               │     │   │
│  │  │  │    1.1     │  │    1.2     │  │    1.3     │  ← Raft Group  │     │   │
│  │  │  │  (Leader)  │  │  (Follower)│  │  (Follower)│               │     │   │
│  │  │  └─────┬──────┘  └─────┬──────┘  └─────┬──────┘               │     │   │
│  │  │        │                │                │                     │     │   │
│  │  │        └────────────────┴────────────────┘                     │     │   │
│  │  │                        ▼                                       │     │   │
│  │  │  ┌─────────────────────────────────────────────────┐           │     │   │
│  │  │  │  KeyValue Store (逻辑上按 shard 组织)            │           │     │   │
│  │  │  │  ┌─────────┐ ┌─────────┐ ┌─────────┐           │           │     │   │
│  │  │  │  │Shard 0  │ │Shard 1  │ │Shard 2  │  ...      │           │     │   │
│  │  │  │  │hash: 0-0│ │hash: 1-1│ │hash: 2-2│           │           │     │   │
│  │  │  │  │ k0,k1.. │ │ k10,k11.│ │ k20,k21.│           │           │     │   │
│  │  │  │  └─────────┘ └─────────┘ └─────────┘           │           │     │   │
│  │  │  └─────────────────────────────────────────────────┘           │     │   │
│  │  └────────────────────────────────────────────────────────────────┘     │   │
│  │                                                                          │   │
│  │  ┌────────────────────────────────────────────────────────────────┐     │   │
│  │  │  ShardGroup GID=2  (拥有 shard 3,4,5)                           │     │   │
│  │  │  ┌────────────┐  ┌────────────┐  ┌────────────┐               │     │   │
│  │  │  │   Server   │  │   Server   │  │   Server   │  ← Raft Group  │     │   │
│  │  │  │    2.1     │  │    2.2     │  │    2.3     │               │     │   │
│  │  │  └─────────────────────────────────────────────────────────────┘     │   │
│  │  └────────────────────────────────────────────────────────────────┘     │   │
│  │                                                                          │   │
│  │  ┌────────────────────────────────────────────────────────────────┐     │   │
│  │  │  ShardGroup GID=3  (拥有 shard 6,7,8,9)                         │     │   │
│  │  │  ┌────────────┐  ┌────────────┐  ┌────────────┐               │     │   │
│  │  │  │   Server   │  │   Server   │  │   Server   │  ← Raft Group  │     │   │
│  │  │  │    3.1     │  │    3.2     │  │    3.3     │               │     │   │
│  │  │  └─────────────────────────────────────────────────────────────┘     │   │
│  │  └────────────────────────────────────────────────────────────────┘     │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  关键设计概念:                                                                  │
│  1. ShardGroup 本质上就是一个 KVRaft 服务，有自己的 Raft group 和状态机        │
│  2. Shard 是逻辑概念：通过 hash(key) % NShards 计算，不涉及物理存储分离         │
│  3. 一个 ShardGroup 可以同时拥有多个 shard，存储在同一个 KeyValue map 中        │
│  4. 迁移时按 shard 粒度进行，同一个 group 的不同 shard 迁移互不影响              │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**请求路由示例：**

```
Client.Put("apple", "red", 0)
    │
    ├─> ShardCtrler.Query() → Config {Shards: [1,1,1,2,2,2,3,3,3,3], Groups: {...}}
    │
    ├─> shard = Key2Shard("apple") = hash("apple") % 10 = 假设为 3
    │
    ├─> gid = Shards[3] = 2
    │
    └─> ShardGrp(GID=2).Put("apple", "red", 0)
            │
            └─> GID=2 的 Raft Group 处理 Put 请求
```

## 数据结构设计

### 1. KVServer 结构 (shardgrp/server.go)

```go
type KVServer struct {
    me   int
    dead int32
    rsm  *rsm.RSM
    gid  tester.Tgid

    mu       sync.Mutex
    KeyValue map[string]ValueEntry  // 实际存储的键值对

    // shard 迁移相关状态
    frozenShards   map[shardcfg.Tshid]bool          // 哪些 shard 被冻结（正在迁移）
    maxShardNum    map[shardcfg.Tshid]shardcfg.Tnum // 每个 shard 的最大配置号（幂等性检查）
    migratedShards map[shardcfg.Tshid]bool          // 哪些 shard 已被迁移走
}
```

**设计说明：**
- `frozenShards`: 标记正在迁移中的 shard，冻结期间拒绝该 shard 的 Get/Put 操作
- `maxShardNum`: 记录每个 shard 处理过的最大配置号，用于判断请求是旧请求、重复请求还是新请求
- `migratedShards`: 标记已经迁移走的 shard，后续操作应返回 ErrWrongGroup

### 2. ShardCtrler 结构 (shardctrler/shardctrler.go)

```go
type ShardCtrler struct {
    clnt *tester.Clnt
    IKVClerk  // 使用 kvraft 存储配置信息
}
```

配置存储在 kvraft 中，使用特殊的 key：
- `config-N`: 存储配置号 N 的完整配置
- `latest`: 存储当前最新的配置号

## 分片迁移流程

### 完整迁移示意图（多 Shard 场景）

假设配置从 Config 10 变更为 Config 11：
- **Config 10**: GID=1 拥有 shard 0,1,2,3；GID=2 拥有 shard 4,5,6,7,8,9
- **Config 11**: GID=1 拥有 shard 0,1；GID=2 拥有 shard 4,5,6,7,8,9；**shard 2,3 迁移到 GID=3**

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                     多 Shard 迁移流程 (shard 2 和 shard 3)                           │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ════════════════════════════════════════════════════════════════════════════════  │
│  初始状态：Config 10                                                                │
│  ════════════════════════════════════════════════════════════════════════════════  │
│                                                                                     │
│  ┌──────────────────────┐  ┌──────────────────────┐  ┌──────────────────────┐      │
│  │     GID=1            │  │     GID=2            │  │     GID=3            │      │
│  │  Shards: [0,1,2,3]   │  │  Shards: [4,5,6,7,8,9]│  │  Shards: []          │      │
│  │                      │  │                      │  │                      │      │
│  │  ┌────┐ ┌────┐       │  │  ┌────┐ ┌────┐      │  │                      │      │
│  │  │Sh0 │ │Sh1 │       │  │  │Sh4 │ │Sh5 │      │  │   (空 Group)         │      │
│  │  │k0  │ │k10 │       │  │  │k40 │ │k50 │      │  │                      │      │
│  │  │k1  │ │k11 │       │  │  │k41 │ │k51 │      │  │                      │      │
│  │  └────┘ └────┘       │  │  └────┘ └────┘      │  │                      │      │
│  │  ┌────┐ ┌────┐       │  │  ┌────┐ ┌────┐      │  │                      │      │
│  │  │Sh2 │ │Sh3 │       │  │  │Sh6 │ │Sh7 │      │  │                      │      │
│  │  │k20 │ │k30 │  ◀────┼──┼──│k60 │ │k70 │      │  │                      │      │
│  │  │k21 │ │k31 │      │  │  │k61 │ │k71 │      │  │                      │      │
│  │  └────┘ └────┘       │  │  └────┘ └────┘      │  │                      │      │
│  └──────────────────────┘  └──────────────────────┘  └──────────────────────┘      │
│                                                                                     │
│  ════════════════════════════════════════════════════════════════════════════════  │
│  目标状态：Config 11 (需要迁移 shard 2,3 从 GID=1 到 GID=3)                         │
│  ════════════════════════════════════════════════════════════════════════════════  │
│                                                                                     │
│  ┌──────────────────────┐  ┌──────────────────────┐  ┌──────────────────────┐      │
│  │     GID=1            │  │     GID=2            │  │     GID=3            │      │
│  │  Shards: [0,1]       │  │  Shards: [4,5,6,7,8,9]│  │  Shards: [2,3]       │      │
│  └──────────────────────┘  └──────────────────────┘  └──────────────────────┘      │
│                                                                                     │
│  ════════════════════════════════════════════════════════════════════════════════  │
│  迁移过程：按 shard 顺序迁移，shard 2 和 shard 3 互不干扰                            │
│  ════════════════════════════════════════════════════════════════════════════════  │
│                                                                                     │
│  ┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓  │
│  ┃ Shard 2 的迁移流程                                                             ┃  │
│  ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛  │
│                                                                                     │
│  Step 1: FreezeShard(shard=2, num=11) on GID=1                                    │
│  ┌──────────────────────┐                                                         │
│  │     GID=1            │                                                         │
│  │  ┌────┐ ┌────┐       │                                                         │
│  │  │Sh0 │ │Sh1 │       │   ← Sh0, Sh1 正常服务                                   │
│  │  └────┘ └────┘       │                                                         │
│  │  ┌────┐ ┌────┐       │                                                         │
│  │  │Sh2 │ │Sh3 │       │   ← Sh2 被冻结 (❄️)                                      │
│  │  │ ❄️ │ │    │       │       收集数据: {k20:v20, k21:v21}                       │
│  │  └────┘ └────┘       │       设置 frozenShards[2]=true                          │
│  └──────────────────────┘                                                         │
│                                                                                     │
│  → 客户端向 GID=1 访问 shard 2 → 返回 ErrWrongGroup                                 │
│  → 客户端向 GID=1 访问 shard 3 → 正常服务 ✅ (shard 3 未冻结)                        │
│                                                                                     │
│  ───────────────────────────────────────────────────────────────────────────────  │
│                                                                                     │
│  Step 2: InstallShard(shard=2, state, num=11) on GID=3                            │
│           ┌──────────────────────┐                                                  │
│  数据 ───▶│     GID=3            │                                                  │
│  {k20:v20} │  ┌────┐             │   ← 安装数据到本地                                │
│  {k21:v21} │  │Sh2 │             │       清除 frozenShards[2]                        │
│           │  │ ✅ │             │       清除 migratedShards[2]                       │
│           │  └────┘             │   → Sh2 准备就绪                                   │
│           └──────────────────────┘                                                  │
│                                                                                     │
│  ───────────────────────────────────────────────────────────────────────────────  │
│                                                                                     │
│  Step 3: DeleteShard(shard=2, num=11) on GID=1                                     │
│  ┌──────────────────────┐                                                         │
│  │     GID=1            │                                                         │
│  │  ┌────┐ ┌────┐       │                                                         │
│  │  │Sh0 │ │Sh1 │       │                                                         │
│  │  └────┘ └────┘       │                                                         │
│  │  ┌────┐ ┌────┐       │                                                         │
│  │  │Sh2 │ │Sh3 │       │   ← 删除 Sh2 数据                                       │
│  │  │ 🗑️ │ │    │       │       设置 migratedShards[2]=true                       │
│  │  └────┘ └────┘       │       清除 frozenShards[2]                              │
│  └──────────────────────┘   → Sh2 迁移完成！                                        │
│                                                                                     │
│  ┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓  │
│  ┃ Shard 3 的迁移流程 (与 Shard 2 并行，互不干扰)                                  ┃  │
│  ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛  │
│                                                                                     │
│  Step 1: FreezeShard(shard=3, num=11) on GID=1                                    │
│  ┌──────────────────────┐                                                         │
│  │     GID=1            │                                                         │
│  │  ┌────┐ ┌────┐       │                                                         │
│  │  │Sh0 │ │Sh1 │       │   ← Sh0, Sh1 正常服务                                   │
│  │  └────┘ └────┘       │       (Sh2 已被删除)                                    │
│  │  ┌────┐              │                                                         │
│  │  │Sh3 │              │   ← Sh3 被冻结 (❄️)                                      │
│  │  │ ❄️ │              │       收集数据: {k30:v30, k31:v31}                       │
│  │  └────┘              │       设置 frozenShards[3]=true                          │
│  └──────────────────────┘                                                         │
│                                                                                     │
│  → 客户端向 GID=1 访问 shard 3 → 返回 ErrWrongGroup                                 │
│  → 客户端向 GID=3 访问 shard 2 → 正常服务 ✅ (shard 2 已安装)                        │
│                                                                                     │
│  ───────────────────────────────────────────────────────────────────────────────  │
│                                                                                     │
│  Step 2: InstallShard(shard=3, state, num=11) on GID=3                            │
│           ┌──────────────────────┐                                                  │
│  数据 ───▶│     GID=3            │                                                  │
│  {k30:v30} │  ┌────┐ ┌────┐     │   ← 安装数据，Sh2 和 Sh3 共存                    │
│  {k31:v31} │  │Sh2 │ │Sh3 │     │       清除 frozenShards[3]                        │
│           │  │ ✅ │ │ ✅ │     │       清除 migratedShards[3]                       │
│           │  └────┘ └────┘     │   → Sh3 准备就绪                                   │
│           └──────────────────────┘                                                  │
│                                                                                     │
│  ───────────────────────────────────────────────────────────────────────────────  │
│                                                                                     │
│  Step 3: DeleteShard(shard=3, num=11) on GID=1                                     │
│  ┌──────────────────────┐                                                         │
│  │     GID=1            │                                                         │
│  │  ┌────┐ ┌────┐       │                                                         │
│  │  │Sh0 │ │Sh1 │       │   ← GID=1 现在只拥有 Sh0, Sh1                            │
│  │  └────┘ └────┘       │                                                         │
│  │  ┌────┐              │   ← 删除 Sh3 数据                                       │
│  │  │Sh3 │              │       设置 migratedShards[3]=true                       │
│  │  │ 🗑️ │              │       清除 frozenShards[3]                              │
│  │  └────┘              │   → Sh3 迁移完成！                                        │
│  └──────────────────────┘                                                         │
│                                                                                     │
│  ════════════════════════════════════════════════════════════════════════════════  │
│  最终状态：Config 11 迁移完成                                                       │
│  ════════════════════════════════════════════════════════════════════════════════  │
│                                                                                     │
│  ┌──────────────────────┐  ┌──────────────────────┐  ┌──────────────────────┐      │
│  │     GID=1            │  │     GID=2            │  │     GID=3            │      │
│  │  Shards: [0,1]       │  │  Shards: [4,5,6,7,8,9]│  │  Shards: [2,3]       │      │
│  │                      │  │                      │  │                      │      │
│  │  ┌────┐ ┌────┐       │  │  ┌────┐ ┌────┐      │  │  ┌────┐ ┌────┐      │      │
│  │  │Sh0 │ │Sh1 │       │  │  │Sh4 │ │Sh5 │      │  │  │Sh2 │ │Sh3 │      │      │
│  │  │k0  │ │k10 │       │  │  │k40 │ │k50 │      │  │  │k20 │ │k30 │      │      │
│  │  │k1  │ │k11 │       │  │  │k41 │ │k51 │      │  │  │k21 │ │k31 │      │      │
│  │  └────┘ └────┘       │  │  └────┘ └────┘      │  │  └────┘ └────┘      │      │
│  └──────────────────────┘  └──────────────────────┘  └──────────────────────┘      │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### 关键观察

**1. 按 shard 粒度迁移**
- 每个 shard 独立完成 Freeze → Install → Delete 流程
- shard 2 和 shard 3 可以并行迁移（取决于 ChangeConfigTo 的实现）

**2. 同一 Group 内不同 Shard 互不影响**
```
时间线示例：
  t0: Shard 2 开始 Freeze
  t1: Shard 2 冻结中，Shard 3 仍可正常服务 ✅
  t2: Shard 2 开始 Install，Shard 3 开始 Freeze
  t3: Shard 2 迁移完成，Shard 3 仍在迁移中
  t4: Shard 3 迁移完成 ✅
```

**3. 客户端请求处理**
```
不同时间点的请求路由：
- t0-t1 (Shard 2 冻结中，Shard 3 正常):
  Get(k20) → GID=1 → ErrWrongGroup → 重试 → GID=3 ✅
  Get(k30) → GID=1 → OK ✅

- t2-t3 (Shard 2 已迁移，Shard 3 冻结中):
  Get(k20) → GID=1 → ErrWrongGroup(migrated) → 重试 → GID=3 ✅
  Get(k30) → GID=1 → ErrWrongGroup(frozen) → 重试 → GID=3 (可能还在冻结)

- t4 (都迁移完成):
  Get(k20) → GID=3 → OK ✅
  Get(k30) → GID=3 → OK ✅
```

**4. 状态管理的隔离性**
```go
// GID=1 的状态（迁移完成后）
frozenShards:   {}           // 所有 shard 都已解冻
migratedShards: {2: true, 3: true}  // 标记已迁移的 shard
maxShardNum:    {0: 10, 1: 10, 2: 11, 3: 11}  // 每个 shard 的最大配置号

// GID=3 的状态（迁移完成后）
frozenShards:   {}           // 安装后解除冻结
migratedShards: {}           // 清除迁移标记
maxShardNum:    {2: 11, 3: 11}  // 记录已处理的配置号
```

### 关键 RPC 接口

```go
// FreezeShard: 冻结分片并收集数据
type FreezeShardArgs struct {
    Shard Tshid  // 要冻结的 shard
    Num   Tnum   // 配置号（用于幂等性检查）
}
type FreezeShardReply struct {
    State []byte // 序列化的 shard 数据
    Num   Tnum   // 配置号
    Err   Err
}

// InstallShard: 安装分片数据
type InstallShardArgs struct {
    Shard Tshid  // 要安装的 shard
    State []byte // 序列化的 shard 数据
    Num   Tnum   // 配置号
}
type InstallShardReply struct {
    Err Err
}

// DeleteShard: 删除分片数据
type DeleteShardArgs struct {
    Shard Tshid // 要删除的 shard
    Num   Tnum  // 配置号
}
type DeleteShardReply struct {
    Err Err
}
```

### 按 Shard 迁移的设计思想

**为什么选择按 shard 迁移而不是按 group 迁移？**

| 方案 | 优点 | 缺点 |
|------|------|------|
| **按 group 迁移** | 实现简单，一次性迁移整个 group | 迁移数据量大，停机时间长，无法部分迁移 |
| **按 shard 迁移** | ✅ 粒度细，可并行迁移 ✅ 部分 shard 迁移时其他 shard 仍可服务 ✅ 支持负载均衡的平滑调整 | 实现复杂，需要处理部分迁移状态 |

**按 shard 迁移的关键优势：**

1. **最小化服务中断**
   ```
   场景：GID=1 需要迁出 4 个 shard (2,3,4,5)

   按 group 迁移：
     - Freeze 所有数据 → 迁移 → Delete
     - GID=1 在整个迁移期间无法服务 ❌

   按 shard 迁移：
     - Shard 2 迁移中，Shard 3,4,5 仍可服务 ✅
     - 客户端可以继续访问未迁移的 shard
   ```

2. **支持渐进式负载均衡**
   ```
   配置变更序列：
     Config 10: GID=1 [0,1,2,3], GID=2 [4,5,6,7,8,9]
     Config 11: GID=1 [0,1], GID=2 [2,3,4,5,6,7,8,9]  // 迁移 2,3
     Config 12: GID=1 [0], GID=2 [1,2,3,4,5,6,7,8,9]   // 再迁移 1

   每次只迁移部分 shard，逐步将负载转移
   ```

3. **提高迁移并行度**
   ```
   ChangeConfigTo 的实现：
     for shard := 0; shard < NShards; shard++ {
         if 需要迁移 {
             migrateShard(fromGid, toGid, shard, num, new)
         }
     }

     不同 shard 可以独立迁移，互不阻塞
   ```

**数据结构设计支持按 shard 迁移：**

```go
// Per-shard 状态管理
type KVServer struct {
    frozenShards   map[Tshid]bool   // 每个 shard 独立冻结
    maxShardNum    map[Tshid]Tnum   // 每个 shard 独立的配置号
    migratedShards map[Tshid]bool   // 每个 shard 独立的迁移标记
}

// DoOp 中的检查
shard := Key2Shard(key)  // 首先计算 shard
if frozen, ok := kv.frozenShards[shard]; ok && frozen {
    return ErrWrongGroup  // 只影响这个 shard
}
// 其他 shard 的请求正常处理
```

这种设计确保了迁移过程中系统的可用性，是 ShardKV 能够线性扩展的关键。

## 核心代码实现

### 1. 冻结分片 (doFreezeShard)

```go
func (kv *KVServer) doFreezeShard(args *shardrpc.FreezeShardArgs) shardrpc.FreezeShardReply {
    // 幂等性检查：根据 args.Num 与 maxShardNum 的关系判断
    if maxNum, ok := kv.maxShardNum[args.Shard]; ok {
        if args.Num < maxNum {
            // 旧请求：配置号已过期，拒绝
            return ErrMaybe
        } else if args.Num == maxNum {
            // 重复请求：重新收集数据返回
            if migrated, ok := kv.migratedShards[args.Shard]; ok && migrated {
                // shard 已被迁移走，返回空数据
                return OK
            }
            // 重新收集并返回数据
        }
    }

    // 新请求：冻结 shard
    kv.frozenShards[args.Shard] = true
    kv.maxShardNum[args.Shard] = args.Num

    // 收集该 shard 的所有数据
    shardData := make(map[string]ValueEntry)
    for key, entry := range kv.KeyValue {
        if shardcfg.Key2Shard(key) == args.Shard {
            shardData[key] = entry
        }
    }

    state, _ := json.Marshal(shardData)
    return FreezeShardReply{State: state, Num: args.Num, Err: OK}
}
```

**幂等性处理：**
- `args.Num < maxNum`: 旧配置的请求，拒绝
- `args.Num == maxNum`: 重复请求，重新收集数据
- `args.Num > maxNum`: 新请求，正常处理

### 2. 安装分片 (doInstallShard)

```go
func (kv *KVServer) doInstallShard(args *shardrpc.InstallShardArgs) shardrpc.InstallShardReply {
    // 幂等性检查
    if maxNum, ok := kv.maxShardNum[args.Shard]; ok {
        if args.Num < maxNum {
            return ErrMaybe  // 旧请求
        } else if args.Num == maxNum {
            return OK  // 重复请求
        }
    }

    kv.maxShardNum[args.Shard] = args.Num

    // 关键：清除迁移标记（shard 已安装到当前 group）
    delete(kv.migratedShards, args.Shard)

    // 解析并安装数据
    var shardData map[string]ValueEntry
    json.Unmarshal(args.State, &shardData)
    for key, entry := range shardData {
        kv.KeyValue[key] = entry
    }

    // 关键：解除冻结（shard 已安装，可以接受请求）
    delete(kv.frozenShards, args.Shard)

    return OK
}
```

**关键点：**
- 清除 `migratedShards`：shard 安装后属于当前 group，不应被视为"已迁移"
- 解除冻结 `frozenShards`：安装完成后可以接受该 shard 的请求

### 3. 删除分片 (doDeleteShard)

```go
func (kv *KVServer) doDeleteShard(args *shardrpc.DeleteShardArgs) shardrpc.DeleteShardReply {
    // 幂等性检查
    if maxNum, ok := kv.maxShardNum[args.Shard]; ok {
        if args.Num < maxNum {
            return ErrMaybe  // 旧请求，拒绝
        } else if args.Num == maxNum {
            // 重复请求：确保标记已迁移
            kv.migratedShards[args.Shard] = true
            delete(kv.frozenShards, args.Shard)
            return OK
        }
    }

    kv.maxShardNum[args.Shard] = args.Num

    // 标记 shard 已被迁移走
    kv.migratedShards[args.Shard] = true

    // 删除该 shard 的所有数据
    for key := range kv.KeyValue {
        if shardcfg.Key2Shard(key) == args.Shard {
            delete(kv.KeyValue, key)
        }
    }

    // 解冻 shard
    delete(kv.frozenShards, args.Shard)

    return OK
}
```

**关键点：**
- 即使是重复请求也要设置 `migratedShards`，否则后续 Get/Put 不会正确返回 ErrWrongGroup

### 4. Get/Put 操作 (DoOp)

```go
case *rpc.GetArgs:
    shard := shardcfg.Key2Shard(getreq.Key)

    // 检查 1: shard 是否已被迁移走
    if migrated, ok := kv.migratedShards[shard]; ok && migrated {
        return ErrWrongGroup
    }

    // 检查 2: shard 是否被冻结
    if frozen, ok := kv.frozenShards[shard]; ok && frozen {
        return ErrWrongGroup  // 让客户端重新查询配置
    }

    // 正常处理 Get 请求
    // ...

case *rpc.PutArgs:
    shard := shardcfg.Key2Shard(putreq.Key)

    // 检查 1: shard 是否已被迁移走
    if migrated, ok := kv.migratedShards[shard]; ok && migrated {
        return ErrWrongGroup
    }

    // 检查 2: shard 是否被冻结
    if frozen, ok := kv.frozenShards[shard]; ok && frozen {
        return ErrWrongGroup  // 让客户端重新查询配置
    }

    // 正常处理 Put 请求
    // ...
```

**返回 ErrWrongGroup 的原因：**
- 返回 `ErrWrongGroup` 会让客户端重新查询配置
- 客户端会获取新配置，发现 shard 已迁移到其他 group
- 然后向正确的 group 发送请求

### 5. 配置变更 (ChangeConfigTo)

```go
func (sck *ShardCtrler) ChangeConfigTo(new *shardcfg.ShardConfig) {
    // 1. 获取当前配置
    old := sck.Query()

    // 2. 遍历所有 shard，找出需要迁移的
    for shard := 0; shard < NShards; shard++ {
        oldGid := old.Shards[shard]
        newGid := new.Shards[shard]

        if oldGid == newGid {
            continue  // 无需迁移
        }

        if oldGid != 0 && newGid != 0 {
            // 从 oldGid 迁移到 newGid
            sck.migrateShard(oldGid, newGid, shard, new.Num, new)
        } else if oldGid != 0 {
            // Shard 被移除
            sck.removeShard(oldGid, shard, new.Num)
        }
        // newGid != 0 && oldGid == 0: 新 shard，无需迁移
    }

    // 3. 保存新配置
    configStr := new.String()
    sck.IKVClerk.Put("config-"+fmt.Sprint(new.Num), configStr, 0)

    // 4. 更新 latest 指针
    sck.IKVClerk.Put("latest", fmt.Sprint(new.Num), latestVersion)
}

func (sck *ShardCtrler) migrateShard(fromGid, toGid tester.Tgid, shard Tshid, num Tnum, new *ShardConfig) {
    // 1. 冻结并从源 group 获取数据
    fromCk := shardgrp.MakeClerk(sck.clnt, sck.getServersForGroup(fromGid))
    state, _ := fromCk.FreezeShard(shard, num)

    // 2. 安装到目标 group（使用新配置的服务器列表）
    toServers := new.Groups[toGid]
    toCk := shardgrp.MakeClerk(sck.clnt, toServers)
    toCk.InstallShard(shard, state, num)

    // 3. 从源 group 删除
    fromCk.DeleteShard(shard, num)
}
```

## Debug 过程与难点分析

### 难点 1: Version Rollback 问题

**现象：**
```
[DoOp Put] gid=1 key=k0 shard=10 version=215 OK (updated to 216)
[DoOp Put] gid=7 key=k0 shard=10 version=206 OK (updated to 207)
```

Porcupine 可视化显示 version 从 215 回滚到 206，违反线性一致性。

**原因分析：**
1. FreezeShard 收集了 version=215 的数据
2. 但在数据传输过程中，源 group 继续处理 Put，version 增加到 220+
3. InstallShard 将旧数据（v215）安装到目标 group
4. 客户端访问新 group 时读到旧版本

**解决方案：**
```go
// doFreezeShard 中：冻结 shard
kv.frozenShards[args.Shard] = true

// DoOp 的 Get/Put 中：检查冻结状态
if frozen, ok := kv.frozenShards[shard]; ok && frozen {
    return ErrWrongGroup  // 拒绝访问，让客户端重试
}
```

**关键洞察：** FreezeShard 之后，shard 必须完全停止接收 Get / Put 操作。

### 难点 2: Get 操作不允许返回 ErrMaybe

**现象：**
```
[DoOp Get] gid=1 key=k0 shard=10 ErrMaybe (frozen)
Fatal: 0: Get "k0" err ErrMaybe
```

**原因：**
查看 kvtest.go 中的 GetJson 函数：
```go
func (ts *Test) GetJson(ck IKVClerk, key string, me int, v any) rpc.Tversion {
    if val, ver, err := Get(ts.Config, ck, key, ts.oplog, me); err == rpc.OK {
        // ...
    } else {
        ts.Fatalf("%d: Get %q err %v", me, key, err)  // 只接受 OK
    }
}
```

Get 操作只接受 OK，不允许其他错误（包括 ErrMaybe）。

**解决方案：**
Get 在 freeze 期间返回 ErrWrongGroup，让客户端重新查询配置并重试：
```go
if frozen, ok := kv.frozenShards[shard]; ok && frozen {
    return ErrWrongGroup  // 而不是 ErrMaybe
}
```

### 难点 3: 重复 DeleteShard 请求导致迁移状态不一致

**现象：**
```
[doDeleteShard] gid=9 shard=10 num=17 OLD (maxNum=17) -> OK
[DoOp Get] gid=9 key=k0 shard=10 OK version=200  // 仍然能读取！
```

**原因分析：**
初始 doDeleteShard 代码：
```go
if maxNum, ok := kv.maxShardNum[args.Shard]; ok && args.Num <= maxNum {
    return OK  // 直接返回，没有设置 migratedShards！
}
```

重复请求直接返回 OK，但没有设置 `migratedShards[shard] = true`，导致：
1. frozenShards 可能没有清除
2. migratedShards 没有设置
3. Get/Put 继续成功处理该 shard 的请求

**解决方案：**
```go
if maxNum, ok := kv.maxShardNum[args.Shard]; ok {
    if args.Num < maxNum {
        return ErrMaybe  // 旧请求，拒绝
    } else if args.Num == maxNum {
        // 重复请求：确保标记已迁移
        kv.migratedShards[args.Shard] = true
        delete(kv.frozenShards, args.Shard)
        return OK
    }
}
```

### 难点 4: InstallShard 后 Put 返回 ErrWrongGroup

**现象：**
```
[doInstallShard] gid=1 shard=10 num=17 OK, installed 1 keys
[DoOp Put] gid=1 key=k0 shard=10 version=345 ErrWrongGroup (migrated)
```

shard 10 刚安装到 gid=1，但 Put 返回"已迁移"错误。

**原因分析：**
doInstallShard 没有清除 `migratedShards[shard]`。

可能的情况：之前的某个配置中，shard 10 从 gid=1 迁移到其他 group，导致 `migratedShards[10] = true`。

**解决方案：**
```go
func (kv *KVServer) doInstallShard(args *shardrpc.InstallShardArgs) shardrpc.InstallShardReply {
    // ...

    // 关键：清除迁移标记
    delete(kv.migratedShards, args.Shard)

    // 安装数据
    // ...
}
```

### 难点 5: Freeze 期间的状态管理

**问题：** 如果多个 shard 同时迁移，如何保证状态一致性？

**解决方案：**
1. **Per-shard 冻结：** `frozenShards` 是 per-shard 的，不会影响其他 shard
2. **幂等性检查：** 通过 `maxShardNum` 区分新旧请求
3. **三步提交：** Freeze → Install → Delete 确保原子性

```go
// 状态转换图
Normal → Frozen → (Installed → Ready) OR (Deleted → Migrated)
                ↓
              ErrWrongGroup (客户端重新查询配置)
```

## 关键设计决策

### 1. 为什么 Get/Put 在 freeze 期间都返回 ErrWrongGroup？

而不是：
- **返回 ErrMaybe：** 测试不允许 Get 返回 ErrMaybe
- **阻塞等待：** 会阻塞 RSM，影响其他请求
- **返回旧数据：** 可能导致版本回滚

返回 ErrWrongGroup 让客户端重新查询配置，自然地实现"重定向"到正确的 group。

### 2. 为什么需要 maxShardNum？

**场景：** 网络重试、leader 切换导致请求重复

**maxShardNum 的作用：**
- 区分旧请求（Num < maxNum）、重复请求（Num == maxNum）、新请求（Num > maxNum）
- 确保操作的幂等性

### 3. 为什么需要 migratedShards？

DeleteShard 后，shard 的数据被删除，但 `frozenShards` 被清除。如果没有 `migratedShards`：
- 客户端用旧配置向源 group 发送请求
- 源 group 会返回 ErrNoKey（因为数据已删除）
- 客户端无法区分"key 不存在"和"shard 已迁移"

使用 `migratedShards` 明确标记 shard 已迁移，返回 ErrWrongGroup。

### 4. 配置更新的时机问题

**问题：** 配置何时对客户端可见？

**当前实现：**
```go
// 在所有迁移完成后保存新配置
sck.IKVClerk.Put("config-"+fmt.Sprint(new.Num), configStr, 0)
sck.IKVClerk.Put("latest", fmt.Sprint(new.Num), latestVersion)
```

**权衡：**
- **先迁移后更新：** 配置切换时迁移已完成，但迁移期间旧配置仍然有效
- **先更新后迁移：** 配置立即生效，但客户端可能访问到还未迁移的 shard

当前采用"先迁移后更新"策略，利用 ErrWrongGroup 机制处理过渡期。

## 测试结果

所有 5A 测试通过：

| 测试名称 | 网络 | 耗时 | RPCs | Ops | 结果 |
|---------|------|------|------|-----|------|
| Init and Query | reliable | 0.3s | 36 | 0 | ✅ PASS |
| one shard group | reliable | 7.2s | 2624 | 180 | ✅ PASS |
| a group joins | reliable | 6.9s | 5863 | 180 | ✅ PASS |
| delete | reliable | 1.6s | 4305 | 360 | ✅ PASS |
| basic groups join/leave | reliable | 7.6s | 6895 | 240 | ✅ PASS |
| many groups join/leave | reliable | 10.5s | 5896 | 180 | ✅ PASS |
| many groups join/leave | unreliable | 95.8s | 14434 | 180 | ✅ PASS |
| shutdown | reliable | 5.0s | 3888 | 180 | ✅ PASS |
| progress (1) | reliable | 2.9s | 2128 | 82 | ✅ PASS |
| progress (2) | reliable | 7.3s | 8104 | 522 | ✅ PASS |
| one concurrent clerk | reliable | 20.3s | 14875 | 1326 | ✅ PASS |
| many concurrent clerks | reliable | 20.6s | 23843 | 2204 | ✅ PASS |
| one concurrent clerk | unreliable | 27.4s | 5415 | 104 | ✅ PASS |
| many concurrent clerks | unreliable | 39.0s | 16945 | 892 | ✅ PASS |

**总计：** 14 个测试全部通过，总耗时约 253 秒

**关键测试说明：**
- **one/many concurrent clerk**: 多个客户端并发访问，测试线性一致性
- **unreliable network**: 模拟网络分区、丢包等故障情况
- **progress**: 测试系统在配置变更期间是否能持续提供服务

## 总结

Lab 5A 的核心挑战在于：
1. **保证线性一致性：** shard 迁移过程中不能出现版本回滚
2. **幂等性处理：** 正确处理重复的 Freeze/Install/Delete 请求
3. **状态一致性：** frozenShards、maxShardNum、migratedShards 三者协同工作

关键设计：
- **Freeze 期间拒绝操作：** 通过 ErrWrongGroup 让客户端重新查询配置
- **幂等性检查：** 使用 maxShardNum 区分新旧请求
- **明确的状态标记：** migratedShards 清楚标记 shard 的归属状态

---

# Lab 5B: Controller 容错与恢复

## 实验目标

Lab 5B 在 5A 的基础上增加了 controller 的容错能力：
1. **配置变更的容错性**：当 shard group 临时宕机时，配置变更能够等待重试而不是失败
2. **Controller 恢复机制**：当 controller 在配置变更过程中崩溃/分区时，新的 controller 能够检测并继续未完成的配置变更

## 核心问题分析

### 问题场景

```
时间线：
t0: Controller 开始处理 Config 10 → Config 11 的变更
t1: Controller 保存 Config 11 的配置信息
t2: Controller 开始迁移 shard，但目标 group 宕机
t3: Controller 被分区/崩溃
t4: Shard group 恢复
t5: 新的 Controller 启动，需要继续完成 Config 11 的变更
```

### 5A 实现的问题

在 5A 中，`ChangeConfigTo` 的执行顺序是：
```
1. 执行所有 shard 迁移
2. 保存新配置
3. 更新 latest 指针
```

问题：如果 controller 在步骤 1 和 2 之间崩溃，新配置不会被保存，新的 controller 无法检测到未完成的配置变更。

## 解决方案：预保存配置

### 关键设计

将配置保存**提前到迁移之前**：

```
1. 【新增】先保存新配置（但不更新 latest）
2. 执行所有 shard 迁移
3. 更新 latest 指针（标记配置变更完成）
```

**为什么这样设计有效？**
- 新配置被保存后，即使 controller 崩溃，配置信息也不会丢失
- `latest` 指针仍然指向旧配置，所以客户端不会使用新配置
- 新的 controller 可以通过检测 `config-(latest+1)` 是否存在来发现未完成的配置变更

### 代码实现

```go
func (sck *ShardCtrler) ChangeConfigTo(new *shardcfg.ShardConfig) {
    old := sck.Query()
    if old == nil || old.Num >= new.Num {
        return
    }

    // 关键：先保存新配置（但不更新 latest）
    configStr := new.String()
    configKey := "config-" + fmt.Sprint(new.Num)
    sck.IKVClerk.Put(configKey, configStr, 0)

    _, latestVersion, _ := sck.IKVClerk.Get("latest")

    // 执行迁移
    for shard := 0; shard < NShards; shard++ {
        // ... 迁移逻辑 ...
    }

    // 迁移完成后更新 latest
    sck.IKVClerk.Put("latest", fmt.Sprint(new.Num), latestVersion)
}
```

## Controller 恢复机制

### InitController 实现

```go
func (sck *ShardCtrler) InitController() {
    // 1. 获取当前 latest 配置号
    latestVer, _, _ := sck.IKVClerk.Get("latest")
    latestNum, _ := strconv.Atoi(latestVer)
    nextNum := latestNum + 1

    // 2. 检查是否有下一个配置存在
    nextConfigKey := "config-" + fmt.Sprint(nextNum)
    nextConfigValue, _, err := sck.IKVClerk.Get(nextConfigKey)
    if err != rpc.OK {
        return  // 没有未完成的配置变更
    }

    // 3. 有未完成的配置变更，继续执行
    newCfg := shardcfg.FromString(nextConfigValue)
    oldCfg := sck.Query()

    // 继续执行迁移
    for shard := 0; shard < NShards; shard++ {
        oldGid := oldCfg.Shards[shard]
        newGid := newCfg.Shards[shard]

        if oldGid == newGid {
            continue  // 这个 shard 已完成
        }

        // 迁移未完成的 shard
        if oldGid != 0 && newGid != 0 {
            sck.migrateShard(oldGid, newGid, shard, newCfg.Num, newCfg)
        } else if oldGid != 0 {
            sck.removeShard(oldGid, shard, newCfg.Num)
        }
    }

    // 4. 更新 latest 指针
    sck.IKVClerk.Put("latest", fmt.Sprint(nextNum), latestVersion)
}
```

### 恢复流程图

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Controller 恢复流程                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  场景：Controller 在配置变更过程中崩溃/分区                                    │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  崩溃时的状态                                                         │    │
│  │  - latest = 10                                                       │    │
│  │  - config-11 存在（已保存）                                          │    │
│  │  - shard 2,3 部分迁移完成                                            │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  ─────────────────────────────────────────────────────────────────────────  │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  新 Controller 启动，调用 InitController()                           │    │
│  │                                                                      │    │
│  │  Step 1: 检测未完成的配置                                            │    │
│  │  ┌─────────────────────────────────────────────────────────────┐    │    │
│  │  │ latestVer = "10"                                              │    │    │
│  │  │ nextNum = 11                                                  │    │    │
│  │  │ Get("config-11") → 成功 ✅                                     │    │    │
│  │  │ → 发现未完成的配置变更！                                       │    │    │
│  │  └─────────────────────────────────────────────────────────────┘    │    │
│  │                                                                      │    │
│  │  Step 2: 继续执行迁移                                                │    │
│  │  ┌─────────────────────────────────────────────────────────────┐    │    │
│  │  │ for each shard:                                              │    │    │
│  │  │   if oldCfg.Shards[shard] == newCfg.Shards[shard]:           │    │    │
│  │  │     continue  // 已完成，跳过                                 │    │    │
│  │  │   else:                                                       │    │    │
│  │  │     migrateShard(...)  // 继续迁移                            │    │    │
│  │  └─────────────────────────────────────────────────────────────┘    │    │
│  │                                                                      │    │
│  │  Step 3: 更新 latest 指针                                            │    │
│  │  ┌─────────────────────────────────────────────────────────────┐    │    │
│  │  │ Put("latest", "11", version)                                  │    │    │
│  │  │ → 配置变更完成！                                             │    │    │
│  │  └─────────────────────────────────────────────────────────────┘    │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  ─────────────────────────────────────────────────────────────────────────  │
│                                                                             │
│  最终状态：                                                                  │
│  - latest = 11                                                              │
│  - config-11 完全生效                                                       │
│  - 所有 shard 迁移完成                                                      │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 关键设计点

### 1. 配置保存顺序

```
┌─────────────────────────────────────────────────────────────────┐
│                   配置保存顺序对比                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  5A 实现（无容错）：                                              │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐                   │
│  │ 迁移 shard │ → │保存配置  │ → │更新latest │                   │
│  └──────────┘    └──────────┘    └──────────┘                   │
│      ↓ 崩溃                                                              │
│     配置丢失，无法恢复 ❌                                               │
│                                                                 │
│  5B 实现（容错）：                                                   │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐                   │
│  │保存配置  │ → │ 迁移 shard │ → │更新latest │                   │
│  └──────────┘    └──────────┘    └──────────┘                   │
│      ↓ 崩溃                                                              │
│     配置已保存，可以恢复 ✅                                             │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 2. 幂等性保证

恢复机制必须正确处理部分完成的迁移：

```go
// 关键：每次迁移前检查 shard 是否已经迁移
currentCfg := sck.Query()
currentGid := currentCfg.Shards[shard]
if currentGid != fromGid {
    // shard 已经迁移到其他 group，跳过
    return
}
```

这确保了：
- 如果 shard 已经完全迁移（Freeze → Install → Delete），跳过
- 如果 shard 部分迁移（比如 Freeze 完成，Install 未完成），重新执行会通过幂等性机制处理

### 3. Clerk 的重试机制

ShardGrp Clerk 已经实现了无限重试：

```go
func (ck *Clerk) FreezeShard(s Tshid, num Tnum) ([]byte, Err) {
    for {
        reply := FreezeShardReply{}
        ok := ck.clnt.Call(ck.servers[i], "KVServer.FreezeShard", &args, &reply)

        if ok && reply.Err == OK {
            return reply.State, reply.Err
        }

        // 失败则重试
        i = (i + 1) % len(ck.servers)
        time.Sleep(50 * time.Millisecond)
    }
}
```

这意味着：
- 如果 group 宕机，Clerk 会持续重试
- 当 group 恢复后，请求会自动成功
- 不需要额外的重试逻辑

## 测试结果

| 测试名称 | 网络 | 耗时 | RPCs | Ops | 结果 |
|---------|------|------|------|-----|------|
| Join/Leave while shardgrp down | reliable | 4.4s | 2385 | 120 | ✅ PASS |
| Recover controller | reliable | 20.3s | 9105 | 360 | ✅ PASS |

**测试说明：**
- **TestJoinLeave5B**: 测试当 shard group 宕机时，join/leave 操作能够等待并最终完成
- **TestRecoverCtrler5B**: 测试当 controller 崩溃/分区后，新的 controller 能够恢复并完成未完成的配置变更

## 5B 总结

Lab 5B 的核心挑战在于：
1. **配置变更的原子性**：使用预保存配置策略，确保配置信息不会丢失
2. **恢复机制的幂等性**：正确处理部分完成的迁移，避免重复操作
3. **与 Clerk 重试的配合**：利用 Clerk 的重试机制处理临时的 group 不可用

关键设计：
- **预保存配置**：在迁移之前保存配置，但延迟更新 latest 指针
- **恢复检测**：通过检查 `config-(latest+1)` 是否存在来发现未完成的配置变更
- **幂等迁移**：每次迁移前检查 shard 的当前状态，避免重复操作

---

# Lab 5C: 并发 Controller 与配置编号修复

## 实验目标

Lab 5C 在 5A/5B 的基础上增加了对并发 controller 的支持：
1. **并发 Controller**：多个 controller 可能同时执行 ChangeConfigTo，只有一个应该完成
2. **Controller Partition**：当 controller 被分区时，新的 controller 能够接管并完成配置变更
3. **配置编号修复**：修复 InitConfig 中硬编码配置编号的问题

## 核心问题分析

### 问题 1: 并发 Controller 的竞争条件

**场景：**
```
时间线：
t0: Controller A 开始处理 Config N → Config N+1
t1: Controller B 也开始处理 Config N → Config N+1
t2: 只有其中一个 controller 应该完成配置变更
```

**为什么不会冲突？**

关键在于我们使用了 KVRaft 作为配置存储，而 KVRaft 保证：
- 对于同一个 key 的 Put 操作，只有一个会成功
- 其他的 Put 会因为 version 冲突而返回 ErrVersion

但实际上，我们不需要额外的机制来处理并发 controller，因为：
1. 每个 config 都有唯一的编号（config-0, config-1, config-2, ...）
2. 不同的 config 不会冲突
3. 只有在处理同一个 config 时才可能冲突，但这种情况下的冲突会被 KVRaft 的 CAS 机制处理

### 问题 2: InitConfig 硬编码配置编号

**Bug 发现过程：**

在 5C 测试中，我们发现了一个关键问题：

```go
// setupKVService 中的调用
scfg := shardcfg.MakeShardConfig()  // Num=0
scfg.JoinBalance(...)               // Num 变为 1!
ts.sck.InitConfig(scfg)             // 传入 Num=1 的配置
```

但是 InitConfig 的原始实现：
```go
func (sck *ShardCtrler) InitConfig(cfg *shardcfg.ShardConfig) {
    configStr := cfg.String()
    sck.IKVClerk.Put("config-0", configStr, 0)  // 硬编码 "config-0"！
    sck.IKVClerk.Put("latest", "0", 0)          // 硬编码 "0"！
}
```

**问题：**
- setupKVService 创建的配置 Num=1（因为调用了 JoinBalance）
- 但 InitConfig 硬编码保存为 "config-0"，latest 设为 "0"
- 导致配置编号与实际内容不匹配

**解决方案：**
```go
func (sck *ShardCtrler) InitConfig(cfg *shardcfg.ShardConfig) {
    configStr := cfg.String()
    configKey := "config-" + fmt.Sprint(cfg.Num)  // 使用 cfg.Num
    sck.IKVClerk.Put(configKey, configStr, 0)
    sck.IKVClerk.Put("latest", fmt.Sprint(cfg.Num), 0)  // 使用 cfg.Num
}
```

### 问题 3: InitController 只检查 latest+1

**Bug 发现过程：**

在 TestPartitionControllerJoin5C 中，我们发现：

```
日志输出：
[InitController] latest=0
[InitController] config-1 not found, nothing to recover
[ChangeConfigTo] old.Num=1 new.Num=2  // 说明 Config 1 已存在！
```

**矛盾：**
- InitController 读到 latest=0
- 但 ChangeConfigTo 显示 old.Num=1，说明 Config 1 已经被保存了

**根本原因：**

setupKVService 调用 JoinBalance 后，配置的 Num 变为 1，但由于原始 InitConfig 的 bug：
- config-1 被保存为 "config-0"（错误的 key）
- latest 被设置为 "0"

当后续测试运行时：
- Query() 读取 latest="0"，返回 config-0（实际是 Num=1 的配置）
- ts.join 创建新配置，Num 从 1 递增到 2
- InitController 检查 config-(latest+1) = config-1，但实际应该检查 config-2

**解决方案：**

修改 InitController 检查所有未来的配置：
```go
func (sck *ShardCtrler) InitController() {
    latestVer, _, _ := sck.IKVClerk.Get("latest")
    latestNum, _ := strconv.Atoi(latestVer)

    // 找到存在的最大编号的未完成配置
    var nextNum shardcfg.Tnum
    var nextConfigValue string
    for i := 1; i <= 100; i++ {
        candidateNum := shardcfg.Tnum(latestNum + int(i))
        candidateKey := "config-" + fmt.Sprint(candidateNum)
        value, _, err := sck.IKVClerk.Get(candidateKey)
        if err == rpc.OK {
            // 找到了，记录它，继续找更高的
            nextNum = candidateNum
            nextConfigValue = value
        } else {
            break
        }
    }

    if nextNum == 0 {
        return  // 没有未完成的配置
    }

    // 继续迁移...
}
```

## 并发 Controller 的工作机制

### 场景 1: Controller Partition

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Controller Partition 场景                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  Controller A 开始处理 Config 1 → Config 2                           │    │
│  │  ┌─────────────────────────────────────────────────────────────┐    │    │
│  │  │ 1. Put("config-2", configStr)  ✅ 成功                      │    │    │
│  │  │ 2. 开始迁移 shard...                                        │    │    │
│  │  │ 3. Controller A 被分区！                                   │    │    │
│  │  └─────────────────────────────────────────────────────────────┘    │    │
│  │                                                                      │    │
│  │  状态：                                                               │    │
│  │  - latest = 1                                                        │    │
│  │  - config-2 存在（已保存）                                           │    │
│  │  - 迁移未完成                                                         │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  ─────────────────────────────────────────────────────────────────────────  │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  Controller B 启动，调用 InitController()                            │    │
│  │  ┌─────────────────────────────────────────────────────────────┐    │    │
│  │  │ 1. Get("latest") → "1"                                       │    │    │
│  │  │ 2. 检查 config-2...config-N                                  │    │    │
│  │  │ 3. 发现 config-2 存在！                                      │    │    │
│  │  │ 4. 继续完成 Config 2 的迁移                                   │    │    │
│  │  │ 5. Put("latest", "2", version)  ✅                           │    │    │
│  │  └─────────────────────────────────────────────────────────────┘    │    │
│  │                                                                      │    │
│  │  最终状态：                                                           │    │
│  │  - latest = 2                                                        │    │
│  │  - Config 2 完全生效                                                 │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 场景 2: 并发 Controller

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    并发 Controller 场景                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  Controller A 和 B 同时启动                                          │    │
│  │                                                                      │    │
│  │  Controller A:                                     Controller B:     │    │
│  │  ┌──────────────────────┐                    ┌──────────────────────┐│    │
│  │  │1. Query() → Config 1 │                    │1. Query() → Config 1 ││    │
│  │  │2. JoinBalance()      │                    │2. JoinBalance()      ││    │
│  │  │   → Config 2 (Num=2) │                    │   → Config 2 (Num=2) ││    │
│  │  │3. Put("config-2")    │                    │3. Put("config-2")    ││    │
│  │  │   ✅ 成功            │                    │   ❌ ErrVersion(已存在)││    │
│  │  │4. 开始迁移           │                    │4. 重新检查           ││    │
│  │  │5. Put("latest","2")  │                    │5. 发现 Config 2 已存在││    │
│  │  │   ✅ 成功            │                    │6. 直接返回           ││    │
│  │  └──────────────────────┘                    └──────────────────────┘│    │
│  │                                                                      │    │
│  │  结果：只有 Controller A 完成了配置变更                               │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**关键点：**
- KVRaft 的 Put 操作对于相同的 key 和 version 只会成功一次
- 第二个 controller 会因为 version 冲突而检测到配置已存在
- 第二个 controller 会重新查询，发现配置已被处理，直接返回

## 测试结果

| 测试名称 | 网络 | 耗时 | RPCs | Ops | 结果 |
|---------|------|------|------|-----|------|
| Concurrent ctrlers | reliable | 4.2s | 5439 | 120 | ✅ PASS |
| Concurrent ctrlers | unreliable | 51.9s | 7631 | 120 | ✅ PASS |
| Partition controller in join | reliable | 4.6s | 2438 | 120 | ✅ PASS |
| Controllers with leased leadership | reliable | 26.0s | 11242 | 360 | ✅ PASS |
| Controllers with leased leadership | unreliable | 95.3s | 10437 | 240 | ✅ PASS |
| Controllers with leased leadership (nclnt=5) | reliable | 60.5s | 34478 | 2692 | ✅ PASS |
| Controllers with leased leadership (nclnt=5) | unreliable | 62.3s | 18356 | 1238 | ✅ PASS |

**测试说明：**
- **Concurrent ctrlers**: 测试多个 controller 同时启动时的行为
- **Partition controller in join**: 测试 controller 被分区后新 controller 的接管
- **Controllers with leased leadership**: 测试 controller 在 leader 租约期间的行为
- **nclnt=5**: 测试并发客户端场景下的正确性

## 5C 总结

Lab 5C 的核心挑战在于：
1. **并发 Controller 处理**：多个 controller 可能同时工作，需要保证正确性
2. **配置编号的正确性**：InitConfig 必须使用配置的实际编号而不是硬编码
3. **恢复机制的完备性**：InitController 需要检查所有未来的配置，而不仅仅是 latest+1

关键设计：
- **使用 KVRaft 的幂等性**：利用 KVRaft 对相同 key-version Put 只成功一次的特性
- **修复配置编号**：InitConfig 使用 cfg.Num 而不是硬编码
- **完整的恢复检测**：InitController 检查所有可能的未完成配置

---

# 完整的 Debug 过程与踩坑总结

## 踩坑 1: Version Rollback 问题

**现象：**
```
Porcupine 报告：history is not linearizable
[DoOp Put] gid=1 key=k0 shard=10 version=215 OK (updated to 216)
[DoOp Put] gid=7 key=k0 shard=10 version=206 OK (updated to 207)
```

**原因：**
1. FreezeShard 收集了 version=215 的数据
2. 但在数据传输过程中，源 group 继续处理 Put，version 增加到 220+
3. InstallShard 将旧数据（v215）安装到目标 group
4. 客户端访问新 group 时读到旧版本

**解决思路（用户提供）：**
"当 freezeshard 之后，对应的 shard 不应该允许 put 操作，只能允许 get 操作，是不是这样更合理？"

**最终解决方案：**
```go
// doFreezeShard 中：冻结 shard
kv.frozenShards[args.Shard] = true

// DoOp 的 Get/Put 中：检查冻结状态
if frozen, ok := kv.frozenShards[shard]; ok && frozen {
    return ErrWrongGroup  // 拒绝访问，让客户端重试
}
```

## 踩坑 2: Get 操作不允许返回 ErrMaybe

**现象：**
```
Fatal: 0: Get "k0" err ErrMaybe
```

**原因分析：**
查看 kvtest.go 中的 GetJson 函数，发现 Get 只接受 OK，不允许其他错误。

**解决方案：**
Get 在 freeze 期间返回 ErrWrongGroup，而不是 ErrMaybe：
```go
if frozen, ok := kv.frozenShards[shard]; ok && frozen {
    return ErrWrongGroup  // 而不是 ErrMaybe
}
```

## 踩坑 3: 重复 DeleteShard 请求导致迁移状态不一致

**现象：**
```
[doDeleteShard] gid=9 shard=10 num=17 OLD (maxNum=17) -> OK
[DoOp Get] gid=9 key=k0 shard=10 OK version=200  // 仍然能读取！
```

**原因：**
重复请求直接返回 OK，但没有设置 `migratedShards[shard] = true`。

**解决方案：**
```go
if maxNum, ok := kv.maxShardNum[args.Shard]; ok {
    if args.Num < maxNum {
        return ErrMaybe  // 旧请求，拒绝
    } else if args.Num == maxNum {
        // 重复请求：确保标记已迁移
        kv.migratedShards[args.Shard] = true
        delete(kv.frozenShards, args.Shard)
        return OK
    }
}
```

## 踩坑 4: InstallShard 后 Put 返回 ErrWrongGroup

**现象：**
```
[doInstallShard] gid=1 shard=10 num=17 OK, installed 1 keys
[DoOp Put] gid=1 key=k0 shard=10 version=345 ErrWrongGroup (migrated)
```

**原因：**
doInstallShard 没有清除 `migratedShards[shard]`。

**解决方案：**
```go
func (kv *KVServer) doInstallShard(args *shardrpc.InstallShardArgs) shardrpc.InstallShardReply {
    // ...
    // 关键：清除迁移标记
    delete(kv.migratedShards, args.Shard)
    // ...
}
```

## 踩坑 5: InitConfig 硬编码配置编号

**现象：**
```
TestPartitionControllerJoin5C 失败
[InitController] latest=0
[ChangeConfigTo] old.Num=1 new.Num=2  // 矛盾！
```

**原因：**
setupKVService 创建的配置 Num=1，但 InitConfig 硬编码保存为 "config-0"。

**解决方案：**
```go
func (sck *ShardCtrler) InitConfig(cfg *shardcfg.ShardConfig) {
    configKey := "config-" + fmt.Sprint(cfg.Num)  // 使用 cfg.Num
    sck.IKVClerk.Put(configKey, configStr, 0)
    sck.IKVClerk.Put("latest", fmt.Sprint(cfg.Num), 0)  // 使用 cfg.Num
}
```

## 踩坑 6: InitController 只检查 latest+1

**现象：**
```
[InitController] latest=0
[InitController] config-1 not found, nothing to recover
但 config-2 实际存在！
```

**原因：**
由于 InitConfig 的 bug，配置编号跳跃了。InitController 只检查 latest+1，找不到 config-2。

**解决方案：**
检查所有未来的配置，找到最大的未完成配置：
```go
for i := 1; i <= 100; i++ {
    candidateNum := shardcfg.Tnum(latestNum + int(i))
    candidateKey := "config-" + fmt.Sprint(candidateNum)
    value, _, err := sck.IKVClerk.Get(candidateKey)
    if err == rpc.OK {
        nextNum = candidateNum
        nextConfigValue = value
    } else {
        break
    }
}
```

## 踩坑 7: Put 在 freeze 期间返回 ErrMaybe 导致卡死

**现象：**
```
测试卡死不动
```

**原因：**
Put 在 freeze 期间返回 ErrMaybe，客户端不断重试但没有进展。

**解决方案：**
Put 也返回 ErrWrongGroup，让客户端重新查询配置：
```go
if frozen, ok := kv.frozenShards[shard]; ok && frozen {
    return ErrWrongGroup  // 而不是 ErrMaybe
}
```

## 总结：关键设计原则

1. **Freeze 期间完全拒绝操作**：Get 和 Put 都返回 ErrWrongGroup
2. **幂等性是关键**：所有 RPC 操作都需要正确处理重复请求
3. **状态一致性**：frozenShards、maxShardNum、migratedShards 必须协同更新
4. **配置编号要正确**：使用配置的实际编号，不要硬编码
5. **恢复要全面**：检查所有可能的未完成配置

