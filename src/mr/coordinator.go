package mr

import "log"
import "net"
import "os"
import "net/rpc"
import "net/http"
import "time"
// import "fmt"

/*
number of max worker
*/
const NWORKER int = 10

type taskStatus = int
type SchedulePhase = int
type JobType = int


const (
	Idle 		taskStatus = iota
	Paused
	Running
	Finished
)

const (
	Map 		SchedulePhase =  iota
	Reduce
	Done
)

const (
	MapJob		JobType = iota
	ReduceJob
	WaitJob
	CompleteJob
)


type Task struct {
	FileName 	string
	Id 			int
	StartTime	time.Time
	Sstatus		taskStatus
}
type heartbeatMsg struct {
	Rresponse 	*HeartbeatResponse
	Ok 			chan struct{}
}

type reportMsg struct {
	Request *ReportRequest
	Ok		chan struct{}
}

type Coordinator struct {
	// Your definitions here.
	files 		[]string
	NnReduce 	int
	nMap 		int
	phase 		SchedulePhase
	tasks 		[]Task

	heartbeatCh 	chan heartbeatMsg
	reportCh		chan reportMsg
	doneCh			chan struct{}
}

type ReportRequest struct {
	JjobType		JobType
	TtaskId		int
	Sstatus 		taskStatus
}

type ReportResponse struct {
	JjobType		JobType
	TtaskId		int
}	// no use

type HeartbeatRequest struct {
	JjobType		JobType
	TtaskId		int
}	// no use

type HeartbeatResponse struct {
	JjobType		JobType
	TtaskId		int 		// real TtaskId, 真正的 task Id, map 和 reduce task 都各自从 0 开始计数
	TtaskPtr		*Task
	NnReduce     int
}




// Your code here -- RPC handlers for the worker to call.

//
// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
//
func (c *Coordinator) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}


//
// start a thread that listens for RPCs from worker.go
//
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

//
// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
//
func (c *Coordinator) Done() bool {

	// Your code here.
	time.Sleep(time.Second * 1)
	return (c.phase == Done)
}

func (c *Coordinator) HeartBeat(request *HeartbeatRequest, response *HeartbeatResponse) error {
	msg := heartbeatMsg{response, make(chan struct{})}
	c.heartbeatCh <- msg
	<- msg.Ok
	// fmt.Println("HearBeat Begin ... msg = ", msg)
	// clean up msg.Ok channel & msg.Ok is ready
	return nil
}

func (c *Coordinator) Report(request *ReportRequest, rresponse *ReportResponse) error {
	msg := reportMsg{request, make(chan struct{})}
	c.reportCh <- msg
	<- msg.Ok
	// fmt.Println("Report Begin ... msg = ", msg)
	// clean up msg.Ok channel & msg.Ok is ready
	return nil
}

func (c *Coordinator) schedule() {
	// c.initMapPhase()
	// fmt.Println("000 c.phase = ", c.phase)
	// fmt.Println("001 Begin = ", Begin)
	// fmt.Println("002 Map = ", Map)
	// fmt.Println("003 Reduce = ", Reduce)
	// fmt.Println("004 Done = ", Done)
	for {
		// fmt.Println("005 ...")
		select {
		case msg := <- c.heartbeatCh:
			// asking for a map / reduce task
			// fmt.Println("c.phase = ", c.phase)
			if c.phase == Done {
				msg.Rresponse.JjobType = CompleteJob
				msg.Rresponse.TtaskPtr = nil
				msg.Rresponse.NnReduce = c.NnReduce
				msg.Rresponse.TtaskId = 0
				// fmt.Println("State Done, msg.Rresponse of HeartBeat = ", msg.Rresponse)
			} else if c.phase == Map {
				askTaskFlag := false
				for i := 0; i < c.nMap; i++ {
					if (c.tasks[i].Sstatus == Idle || c.tasks[i].Sstatus == Paused) {
						askTaskFlag = true
						c.tasks[i].Sstatus = Running

						msg.Rresponse.JjobType = MapJob
						msg.Rresponse.TtaskPtr = &(c.tasks[i])
						msg.Rresponse.NnReduce = c.NnReduce
						msg.Rresponse.TtaskId = i
						c.tasks[i].StartTime = time.Now()

						break
					}else if (c.tasks[i].Sstatus == Running) {
						curTime := time.Now()
						if curTime.Unix() - c.tasks[i].StartTime.Unix() > 10 {
							c.tasks[i].StartTime = curTime
							
							askTaskFlag = true

							msg.Rresponse.JjobType = MapJob
							msg.Rresponse.TtaskPtr = &(c.tasks[i])
							msg.Rresponse.NnReduce = c.NnReduce
							msg.Rresponse.TtaskId = i

							break
						}
					}
				}
				if askTaskFlag == false {
					msg.Rresponse.JjobType = WaitJob
					msg.Rresponse.TtaskPtr = nil
					msg.Rresponse.NnReduce = c.NnReduce
					msg.Rresponse.TtaskId = 0
				}
				// fmt.Println("State Map, msg.Rresponse of HeartBeat =  ", msg.Rresponse)
			} else if c.phase == Reduce {
				askTaskFlag := false
				for index := 0; index < c.NnReduce; index++ {
					i := index + c.nMap
					if (c.tasks[i].Sstatus == Idle || c.tasks[i].Sstatus == Paused) {
						askTaskFlag = true
						c.tasks[i].Sstatus = Running

						msg.Rresponse.JjobType = ReduceJob
						msg.Rresponse.TtaskPtr = &(c.tasks[i])
						msg.Rresponse.NnReduce = c.NnReduce
						msg.Rresponse.TtaskId = index
						c.tasks[i].StartTime = time.Now()

						break
					}else if (c.tasks[i].Sstatus == Running) {
						curTime := time.Now()
						if curTime.Unix() - c.tasks[i].StartTime.Unix() > 10 {
							c.tasks[i].StartTime = curTime
							
							askTaskFlag = true

							msg.Rresponse.JjobType = ReduceJob
							msg.Rresponse.TtaskPtr = &(c.tasks[i])
							msg.Rresponse.NnReduce = c.NnReduce
							msg.Rresponse.TtaskId = index

							break
						}
					}
				}
				if askTaskFlag == false {
					msg.Rresponse.JjobType = WaitJob
					msg.Rresponse.TtaskPtr = nil
					msg.Rresponse.NnReduce = c.NnReduce
					msg.Rresponse.TtaskId = 0
				}
				// fmt.Println("State Reduce, msg.Rresponse of HeartBeat =  ", msg.Rresponse)
			}
			msg.Ok <- struct{}{}
		case msg := <- c.reportCh:
			// fmt.Println("2 c.phase =  ", c.phase)
			// a map / reduce task is done, change task info
			taskIdTemp := msg.Request.TtaskId
			// fmt.Println("reported taskId =  ", taskIdTemp)
			// if msg.Request.JjobType == WaitJob {
			// 	msg.Ok <- struct{}{}
			// 	continue
			// }
			if msg.Request.JjobType == ReduceJob {
				taskIdTemp = taskIdTemp + c.nMap
			}
			// if time.Now().Unix() - c.tasks[taskIdTemp].StartTime.Unix() > 10 {
			// 	msg.Ok <- struct{}{}
			// 	continue
			// }
			c.tasks[taskIdTemp].Sstatus = Finished

			// check all tasks, determine whether done
			if (c.phase == Reduce) {
				flag2Change := true
				for i := c.nMap; i < (c.nMap + c.NnReduce); i++ {
					if c.tasks[i].Sstatus != Finished {
						flag2Change = false
						break
					}
				}
				if flag2Change == true {
					c.phase = Done
				}
			}
			// fmt.Println("3 c.phase =  ", c.phase)
			// check all map tasks, determine whether go to reduce
			if (c.phase == Map) {
				flag2Change := true
				for i := 0; i < c.nMap; i++ {
					if c.tasks[i].Sstatus != Finished {
						flag2Change = false
						break
					}
				}
				if flag2Change == true {
					c.phase = Reduce
				}
			}
			
			msg.Ok <- struct{}{}
		}
	}
}

//
// create a Coordinator.
// main/mrcoordinator.go calls this function.
// NnReduce is the number of reduce tasks to use.
//
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := Coordinator{}

	// Your code here.
	c.nMap = len(files)
	c.NnReduce = nReduce
	var i int = 0
	currentTime := time.Now()
	for ; i < c.nMap; i++ {
		c.tasks = append(c.tasks, Task{files[i], i, currentTime, Idle})
		c.files = append(c.files, files[i])
	}
	for ; i < nReduce + c.nMap; i++ {
		c.tasks = append(c.tasks, Task{"", i, currentTime, Idle})
	}
	c.heartbeatCh 	= make(chan heartbeatMsg)
	c.reportCh		= make(chan reportMsg)
	c.doneCh 		= make(chan struct{})
	c.phase 		= Map

	// log.Printf("Initial state of Coordinator =  %+v \n", c)
	go c.schedule()
	c.server()
	return &c
}
