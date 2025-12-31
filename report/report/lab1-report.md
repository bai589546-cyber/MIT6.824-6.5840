# 6.5840 Lab 1: MapReduce 实验报告

## 一、实验概述

### 1.1 实验目标
本实验基于Google的MapReduce论文，实现一个分布式MapReduce系统。该系统包括一个协调器（coordinator）进程和多个工作进程（worker），能够在大规模数据集上并行执行Map和Reduce操作，并具备处理工作节点故障的能力。

### 1.2 实验环境
- **编程语言**: Go
- **通信机制**: RPC (基于Unix域套接字)
- **插件系统**: Go plugin package (动态加载Map/Reduce函数)
- **并发控制**: Goroutines + Channels

### 1.3 系统架构
```
┌─────────────────┐    RPC    ┌─────────────────┐
│   Coordinator   │◄─────────►│     Worker      │
│   (主节点)       │           │   (工作节点)     │
│                 │           │                │
│ • 任务调度       │           │ • 执行Map任务    │
│ • 状态管理       │           │ • 执行Reduce任务 │
│ • 故障检测       │           │ • 定期心跳       │
└─────────────────┘           └─────────────────┘
         ▲                              ▲
         │                              │
    输入文件 (pg-*.txt)             输出文件 (mr-out-*)
```

## 二、系统设计与实现

### 2.1 核心数据结构

#### 2.1.1 Coordinator结构
```go
type Coordinator struct {
    files      []string              // 输入文件列表
    NnReduce   int                  // Reduce任务数量
    nMap       int                  // Map任务数量
    phase      SchedulePhase        // 当前调度阶段
    tasks      []Task               // 任务列表
    heartbeatCh chan heartbeatMsg   // 心跳消息通道
    reportCh   chan reportMsg       // 报告消息通道
    doneCh     chan struct{}        // 完成信号通道
}
```

**设计说明**:
- `files`: 存储所有输入文件，每个文件对应一个Map任务
- `NnReduce`: Reduce任务数量，影响中间数据的分区策略
- `phase`: 调度阶段机状态机，支持Map、Reduce、Done三个阶段
- 使用通道实现并发安全的消息传递

#### 2.1.2 Task结构
```go
type Task struct {
    FileName  string    // 输入文件名（仅Map任务）
    Id        int       // 任务ID
    StartTime time.Time // 任务开始时间
    Sstatus   taskStatus // 任务状态
}
```

**任务状态机**:
- `Idle`: 任务未开始
- `Paused`: 任务暂停（可用于故障恢复）
- `Running`: 任务正在执行
- `Finished`: 任务已完成

### 2.2 协调器核心算法

#### 2.2.1 任务调度算法
协调器的`schedule()`方法采用事件驱动模型：

```go
func (c *Coordinator) schedule() {
    for {
        select {
        case msg := <-c.heartbeatCh:
            // 处理心跳请求，分配任务
            c.assignTask(msg)

        case msg := <-c.reportCh:
            // 处理任务完成报告，更新状态
            c.updateTaskStatus(msg)

        case <-c.doneCh:
            // 任务完成，退出调度循环
            return
        }
    }
}
```

**任务分配策略**:
1. **Map阶段**: 遍历Map任务，优先分配Idle或Paused状态的任务
2. **Reduce阶段**: 遍历Reduce任务，同样优先分配空闲任务
3. **故障处理**: 检测任务超时（10秒），重新分配超时任务

#### 2.2.2 故障容忍机制
- **超时检测**: 每个任务记录开始时间，超过10秒自动重新分配
- **状态恢复**: 失败的任务可被其他Worker接管执行
- **原子性保证**: 通过临时文件机制确保数据一致性

### 2.3 工作节点核心算法

#### 2.3.1 任务执行循环
```go
func Worker(mapf func(string, string) []KeyValue,
           reducef func(string, []string) string) {
    for {
        response := callHeartbeat()
        switch response.JjobType {
        case MapJob:
            doMapTask(mapf, response)
            reportTaskCompletion(MapJob, response.TtaskId)

        case ReduceJob:
            doReduceTask(reducef, response)
            reportTaskCompletion(ReduceJob, response.TtaskId)

        case WaitJob:
            time.Sleep(1 * time.Second)

        case CompleteJob:
            return
        }
    }
}
```

#### 2.3.2 Map任务实现
**执行流程**:
1. **文件读取**: 打开指定的输入文件
2. **Map函数调用**: 对每行内容调用用户Map函数
3. **中间结果排序**: 按键对Map输出进行排序
4. **数据分区**: 使用`ihash(key) % nReduce`进行分区
5. **文件写入**: 为每个Reduce分区创建临时文件
6. **原子重命名**: 完成后重命名为最终文件名

**分区算法**:
```go
func ihash(key string) int {
    h := fnv.New32a()
    h.Write([]byte(key))
    return int(h.Sum32() & 0x7fffffff)
}
```
使用FNV-1a哈希函数确保键的均匀分布。

#### 2.3.3 Reduce任务实现
**执行流程**:
1. **文件收集**: 收集所有Map任务对应的输出文件
2. **数据读取**: 读取并解析JSON格式的中间数据
3. **键排序**: 对所有键值对按键排序
4. **数据聚合**: 将相同键的值聚合到一起
5. **Reduce函数调用**: 对每个键调用用户Reduce函数
6. **结果输出**: 将最终结果写入输出文件

### 2.4 RPC通信设计

#### 2.4.1 接口定义
**心跳接口** (`Heartbeat`):
- **功能**: Worker向Coordinator请求任务
- **请求**: 包含Worker标识
- **响应**: 返回任务类型和任务详情

**报告接口** (`Report`):
- **功能**: Worker向Coordinator报告任务完成状态
- **请求**: 包含任务类型、任务ID、完成状态
- **响应**: 确认接收

#### 2.4.2 通信机制
- **传输方式**: Unix域套接字 (`/var/tmp/5840-mr-{uid}`)
- **序列化**: Go RPC内置的gob编码
- **并发处理**: 每个RPC调用在独立的goroutine中处理

### 2.5 文件系统设计

#### 2.5.1 文件命名规范
- **Map输出**: `mr-out-{mapId}-{reduceId}`
  - 每个Map任务为每个Reduce任务生成一个文件
  - 例如: `mr-out-0-0`, `mr-out-0-1`, `mr-out-1-0`等

- **Reduce输出**: `mr-out-{reduceId}`
  - 每个Reduce任务生成最终输出文件
  - 例如: `mr-out-0`, `mr-out-1`等

#### 2.5.2 原子性保证
```go
// 创建临时文件
tmpFile := fmt.Sprintf("mr-tmp-%d-%d", mapId, reduceId)
file, err := os.Create(tmpFile)

// 写入完成后原子重命名
os.Rename(tmpFile, finalFileName)
```
通过临时文件+原子重命名机制确保写入过程的原子性，避免读取到不完整的数据。

## 三、关键技术分析

### 3.1 并发控制机制

#### 3.1.1 通道同步
- 使用`heartbeatCh`和`reportCh`通道进行消息传递
- 每个消息包含响应通道，实现同步RPC调用
- 避免了复杂的锁机制，提高代码可读性

#### 3.1.2 状态管理
- Coordinator的所有状态更新都通过单线程的调度器进行
- 使用通道确保消息处理的顺序性和原子性
- 避免了竞态条件，保证并发安全

### 3.2 负载均衡策略

#### 3.2.1 动态任务分配
- Worker通过心跳机制主动请求任务
- Coordinator根据当前状态动态分配可用任务
- 实现了工作窃取（work-stealing）的效果

#### 3.2.2 故障恢复
- 超时任务自动重新分配给健康的Worker
- 无需复杂的故障检测机制
- 简单有效的容错策略

### 3.3 性能优化技术

#### 3.3.1 内存管理
- Map任务在内存中完成排序，减少磁盘I/O
- Reduce任务按需读取中间文件，控制内存使用
- 流式处理大数据集，避免内存溢出

#### 3.3.2 I/O优化
- 使用JSON格式进行中间数据序列化
- 批量写入减少系统调用次数
- 临时文件机制避免部分写入的影响

## 四、测试与验证

### 4.1 测试环境
- **测试数据**: Project Gutenberg文本文件 (`pg-*.txt`)
- **应用插件**:
  - `wc.so`: 词频统计应用
  - `indexer.so`: 文本索引应用
  - `crash.so`: 故障模拟应用

### 4.2 测试用例
实验要求通过以下7个测试:

1. **词频统计测试** (`wc test`)
   - 验证基本的MapReduce功能
   - 检查输出正确性

2. **索引生成测试** (`indexer test`)
   - 验证不同应用的兼容性
   - 测试复杂数据处理

3. **Map并行度测试** (`map parallelism test`)
   - 验证多个Map任务可并行执行
   - 检查并发正确性

4. **Reduce并行度测试** (`reduce parallelism test`)
   - 验证多个Reduce任务可并行执行
   - 检查数据分区正确性

5. **任务计数测试** (`job count test`)
   - 验证任务调度的正确性
   - 确保无任务重复执行

6. **早期退出测试** (`early exit test`)
   - 验证Worker能正确检测任务完成并退出
   - 检查资源清理

7. **故障恢复测试** (`crash test`)
   - 使用`crash.so`模拟Worker故障
   - 验证系统能从故障中恢复

### 4.3 正确性验证
```bash
# 验证输出正确性
cat mr-out-* | sort | diff mr-correct-wc.txt -

# 运行完整测试套件
bash test-mr.sh
```

## 五、实验总结

### 5.1 实现特点
1. **简洁性**: 采用清晰的设计，避免了不必要的复杂性
2. **容错性**: 通过超时重分配机制有效处理节点故障
3. **可扩展性**: 支持多Worker并发执行，具备良好的扩展性
4. **原子性**: 临时文件机制确保数据一致性
5. **通用性**: 插件机制支持不同类型的MapReduce应用

### 5.2 关键技术收获
- **分布式系统设计**: 理解了master-slave架构的设计原理
- **并发编程**: 掌握了Go语言的goroutine和channel使用
- **RPC通信**: 实现了基于RPC的节点间通信机制
- **容错设计**: 学习了简单有效的故障容忍策略
- **性能优化**: 了解了分布式系统中的性能优化技术

### 5.3 改进方向
1. **网络通信**: 当前使用Unix域套接字，可扩展为TCP/IP支持跨机器部署
2. **数据本地性**: 可考虑数据本地性优化，减少网络传输
3. ** speculative execution**: 可实现推测执行机制，进一步优化性能
4. **监控和调试**: 可添加更详细的日志和监控机制

### 5.4 实验心得
通过实现MapReduce系统，我深入理解了分布式系统的核心概念和设计原理。这个实验不仅锻炼了我的编程能力，更重要的是让我掌握了：

1. **系统思维**: 如何将复杂的分布式问题分解为简单的组件
2. **并发控制**: 如何在分布式环境中保证数据一致性和并发安全
3. **容错设计**: 如何设计能够容忍节点故障的分布式系统
4. **性能权衡**: 如何在简单性、性能和可靠性之间找到平衡

这些知识和技能对于后续学习更复杂的分布式系统（如Raft、Spanner等）奠定了坚实的基础。

## 六、参考文献
1. Dean, Jeffrey, and Sanjay Ghemawat. "MapReduce: simplified data processing on large clusters." Communications of the ACM 51.1 (2008): 107-113.
2. Go Documentation: https://golang.org/doc/
3. 6.5840 Course Materials: MIT Distributed Systems