package mr

import "log"
import "net"
import "os"
import "net/rpc"
import "net/http"

type Task struct {
	fileName 	string
	id 			int
	startTime	time.Time
	status		TaskStatus
}

type Coordinator struct {
	// Your definitions here.
	files 		[]string
	nReduce 	int
	nMap 		int
	phase 		SchedulePhase
	tasks 		[]Task

	heartbeatCh 	chan heartbeatMsg
	reportCh		chan report reportMsg
	doneCh			chan struct{}
}

type heartbeatMsg struct {
	response 	*HeartbeatResponse
	ok 			chan struct{}
}

type reoprtMsg struct {
	request *ReportRequest
	ok		chan struct{}
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
	ret := false

	// Your code here.


	return ret
}

func (c *Coordinator) Heartbeat(request *HeartbeatRequest, response *HeartbeatResponse) error {
	msg := heartbeatMsg{response, make(chan struct{})}
	c.heartbeatCh <- msg
	<- msg.ok
	return nil
}

func (c *Coordinator) Report(request *ReportRequest, response *ReportResponse) error {
	msg := reoprtMsg{request, make(chan struct{})}
	c.reportCh <- msg
	<- msg.ok
	return nil
}

func (c *Coordinator) schedule() {
	c.initMapPhase()
	for {
		select {
		case msg := <- c.heartbeatCh:
			...
			msg.ok <- struct{}{}
		case msg := <- c.reportCh:
			...
			msg.ok <- struct{}{}
		}
	}
}

//
// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
//
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := Coordinator{}

	// Your code here.


	c.server()
	return &c
}
