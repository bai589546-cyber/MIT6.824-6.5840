package raft

// The file raftapi/raft.go defines the interface that raft must
// expose to servers (or the tester), but see comments below for each
// of these functions for more details.
//
// Make() creates a new raft peer that implements the raft interface.

import (
	//	"bytes"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	//	"6.5840/labgob"
	"6.5840/labrpc"
	"6.5840/raftapi"
	"6.5840/tester1"
	"fmt"
)

// A Go object that represents log entry for raft state machine
type Entry struct {
	Term 		int
	Command 	any
}

type RaftState = int
const (
	Leader		RaftState = iota
	Follower
	Candidate
)

// A Go object implementing a single Raft peer.
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *tester.Persister   // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (3A, 3B, 3C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	// state for vote election (3A) 2025-12-29
	currentTerm	int 				// latest term server has seen (initialized to 0 on first boot, increases monotonically)
	votedFor	int 				// candidateId that received MY vote in current term (or -1 if none)
	logs 		[]Entry 			// array for log entry (for raft state machine, index - term)
	state 		RaftState

	heartbeatCh	chan bool
	


}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {

	var term int
	var isleader bool
	// Your code here (3A).
	term = rf.currentTerm
	isleader = (rf.state == Leader)
	
	return term, isleader
}

// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
// before you've implemented snapshots, you should pass nil as the
// second argument to persister.Save().
// after you've implemented snapshots, pass the current snapshot
// (or nil if there's not yet a snapshot).
func (rf *Raft) persist() {
	// Your code here (3C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// raftstate := w.Bytes()
	// rf.persister.Save(raftstate, nil)
}


// restore previously persisted state.
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (3C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}

// how many bytes in Raft's persisted log?
func (rf *Raft) PersistBytes() int {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.persister.RaftStateSize()
}


// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (3D).

}


// example RequestVote RPC arguments structure.
// field names must start with capital letters!
type RequestVoteArgs struct {
	// Your data here (3A, 3B).
	Term 			int 			// candidate's term
	CandidateId 	int 			// candidate requesting vote
	LastLogIndex 	int 			// index of candidate's last log entry
	LastLogTerm 	int 			// term of candidate's last log entry
}

// example RequestVote RPC reply structure.
// field names must start with capital letters!
type RequestVoteReply struct {
	// Your data here (3A).
	Term 			int 			// currentTerm, for candidate to update itself
	VoteGranted 	bool 			// true means candidate receive vote
}

// example RequestVote RPC handler.
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (3A, 3B).
	rf.mu.Lock()
	defer rf.mu.Unlock()

	

	reply.VoteGranted = false		// initialized as false
	if args.Term < rf.currentTerm {
		reply.VoteGranted = false
	} else {
		if args.Term > rf.currentTerm {
			rf.votedFor = -1 			// 连续 election 发生，需要清除上个 term 留下来的 vote
		}

		if rf.votedFor == args.CandidateId || rf.votedFor == -1 {
			lastLogIndex := len(rf.logs) - 1
			coldStartFlag := (lastLogIndex < 0)
			voteFlag := coldStartFlag
			if !coldStartFlag {
				lastLogTerm := rf.logs[lastLogIndex].Term
				voteFlag = lastLogTerm > args.LastLogTerm || (lastLogTerm == args.LastLogTerm && lastLogIndex >= args.LastLogIndex)
			}
			if voteFlag {
				reply.VoteGranted = true
				rf.votedFor = args.CandidateId
				rf.currentTerm = args.Term
				rf.state = Follower
				fmt.Println("Term ", rf.currentTerm, ", S", rf.me, " voted for S", args.CandidateId)
				
				// 【关键修复】：既然投了票，就承认了对方的地位，必须重置自己的选举定时器！
				// 否则你刚投完票，马上自己超时，又去拆台。
				select {
				case rf.heartbeatCh <- true:
				default:
				}
			}
		}
	}
	reply.Term = rf.currentTerm
}

// example RequestVote RPC arguments structure.
// field names must start with capital letters!
type AppendEntriesArgs struct {
	Term 			int 			// leader's term
	LeaderId 		int 			// leader's me
	PrevLogIndex 	int 			// index of log entry immediately preceding new ones
	PrevLogTerm 	int 			// term of prev log index entry
	LeaderCommit 	int 			// leaderCommit
	Entries 		[]Entry
}

// example RequestVote RPC reply structure.
// field names must start with capital letters!
type AppendEntriesReply struct {
	Term 			int 			// currentTerm, for leader to update itself
	Success 		bool 			// true if follower contained entry matching prevLogIndex and prevLogTerm
}

// example RequestVote RPC handler.
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply){
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// 1. initialized Success to be false
	reply.Term = rf.currentTerm
	reply.Success = false

	// 2. check Term
	if args.Term < rf.currentTerm {
		return
	}

	// 3. if received legal message from Leader
	if args.Term >= rf.currentTerm {
		rf.currentTerm = args.Term
		rf.state = Follower

		select {
		case rf.heartbeatCh <- true:
		default:
		}
	}

	reply.Success = true
}

// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}


func (rf *Raft) StartElection() {
	rf.mu.Lock()

	rf.state = Candidate
	rf.currentTerm = rf.currentTerm + 1
	rf.votedFor = rf.me

	currentTerm := rf.currentTerm
	votes := 1

	fmt.Println("Term", rf.currentTerm, "S", rf.me, "start election...")

	args := RequestVoteArgs{
		Term:		currentTerm,
		CandidateId:	rf.me,
		LastLogIndex: 	0,
		LastLogTerm: 	0,
	}

	rf.mu.Unlock()

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
}

// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (3B).


	return index, term, isLeader
}

// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

func (rf *Raft) ticker() {
    for rf.killed() == false {

		// fmt.Println("Ticker Begin ...")
		// fmt.Println("Ticker server number = ", rf.me)
        // 1. 每次循环开始，先生成一个随机超时 (比如 300~500ms)
        // 这一点至关重要！不能是固定的！
        timeout := rf.getRandomTimeout()
        
        // 2. 创建 Timer
        timer := time.NewTimer(timeout)

        select {
        case <-timer.C:
            // --- 超时逻辑 ---
			rf.mu.Lock()
            state := rf.state
            rf.mu.Unlock()
            if state != Leader {
                // 发起选举 (StartElection 内部负责改变状态、Term++ 等)
                rf.StartElection()
            }
            
            // 注意：timer.C 触发后，timer 自动失效，不需要 Stop
            // 循环回到开头会创建新的 timer，所以这里不需要 Reset

        case <-rf.heartbeatCh:
            // --- 重置逻辑 ---
            // 收到心跳信号，停止当前的 Timer
            if !timer.Stop() {
                select {
                case <-timer.C:
                default:
                }
            }
            // 不需要显式 Reset，因为 break select 后，
            // 下一次 for 循环开头会 create new timer。
            // (这是为了避免你维护 timer 对象的复杂性，直接新建最简单)
        }
    }
}

// 辅助函数：生成随机时间
func (rf *Raft) getRandomTimeout() time.Duration {
    ms := 300 + (rand.Int63() % 300) // 200ms ~ 400ms
    return time.Duration(ms) * time.Millisecond
}

func (rf *Raft) handleAppendEntriesReply(server int, args AppendEntriesArgs, reply AppendEntriesReply) {
	rf.mu.Lock()
	if reply.Term > rf.currentTerm {
		rf.currentTerm = reply.Term 		// <--- MUST update Term
		rf.state = Follower
		rf.votedFor = -1 					// <--- when a leader becomes a follower, vote must be cleared
	}

	defer rf.mu.Unlock()
}

func (rf *Raft) BroadcastHeartbeat() {
	// fmt.Println("BroadcastHearBeat Begin ...")
    rf.mu.Lock()
    if rf.state != Leader {
        rf.mu.Unlock()
        return
    }
    // 记得在不需要锁的时候（比如发 RPC 前）解锁，
    // 或者拷贝一份 peers 列表在锁外遍历
    currentTerm := rf.currentTerm
    rf.mu.Unlock() // 解锁


	for i := range rf.peers {
        if i == rf.me { continue }
        
        // 构造 AppendEntriesArgs (深拷贝)
        args := AppendEntriesArgs{}
		args.Term = currentTerm
		args.LeaderId = rf.me
        
        // 启动 Goroutine 发送 (核心：不阻塞 ticker)
        go func(server int, args AppendEntriesArgs) {
            reply := AppendEntriesReply{}
            if rf.sendAppendEntries(server, &args, &reply) {
                rf.handleAppendEntriesReply(server, args, reply)
            }
        }(i, args)
    }
}

func (rf *Raft) heartbeatTicker() {
	// heartbeat interval = 100ms
	const heartbeatInterval = 100 * time.Millisecond

	// fmt.Println("HearBeat Begin ...")
	fmt.Println("server number = ", rf.me)

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

// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
func Make(peers []*labrpc.ClientEnd, me int,
	persister *tester.Persister, applyCh chan raftapi.ApplyMsg) raftapi.Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (3A, 3B, 3C).

	// 3A: 2025-12-29
	rf.currentTerm = 0			// term = 0
	rf.votedFor = -1			// vote for nobody yet
	rf.state = Follower 		// initially, every server is Follower
	rf.dead = 0					// dead = false -> alive


	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())
	rf.heartbeatCh = make(chan bool, 1)

	// start ticker goroutine to start elections
	go rf.ticker()
	go rf.heartbeatTicker()


	return rf
}
