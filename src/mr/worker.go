package mr

import "fmt"
import "log"
import "net/rpc"
import "hash/fnv"
import "sort"
import "strconv"
import "math/rand"
import "os"
import "encoding/json"
import "time"
import "regexp"
import "path/filepath"
import "io/ioutil"


//
// Map functions return a slice of KeyValue.
//
type KeyValue struct {
	Key   string
	Value string
}

// for sorting by key.
type ByKey []KeyValue

// for sorting by key.
func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

//
// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
//
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

func doHeartbeat() HeartbeatResponse {
	request := HeartbeatRequest{}
	response := HeartbeatResponse{}

	ok := call("Coordinator.HeartBeat", &request, &response)
	if ok == true {
		// fmt.Printf("newly doHeartBeat\n")
	} else {
		fmt.Printf("")
		fmt.Printf("doHeartBeat failed!\n")
	}
	return response
}

func doReport(request ReportRequest) ReportResponse {
	response := ReportResponse{}

	ok := call("Coordinator.Report", &request, &response)
	if ok == true {
		// reply.Y should be 100.
		// fmt.Printf("newly doReport\n")
	} else {
		fmt.Printf("doReport failed!\n")
	}
	return response
}

// random string generator
// used for ATOM file write
const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

var seededRand *rand.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))

func generateRandomString(length int) string {
   b := make([]byte, length)
   for i := range b {
      b[i] = charset[seededRand.Intn(len(charset))]
   }
   return string(b)
}



//
// main/mrworker.go calls this function.
//
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	// Your worker implementation here.
	for{
		response := doHeartbeat()
		// log.Printf("Worker: receive coordinator's heartbeat %+v \n", response)
		switch response.JjobType {
		case MapJob:
			doMapTask(mapf, response)
			request := ReportRequest{MapJob, response.TtaskId, Finished}
			doReport(request)
		case ReduceJob:
			doReduceTask(reducef, response)
			request := ReportRequest{ReduceJob, response.TtaskId, Finished}
			doReport(request)
		case WaitJob:
			time.Sleep(1 * time.Second)
			// request := ReportRequest{WaitJob, response.TtaskId, Idle}
			// doReport(request)
		case CompleteJob:
			return
		default:
			panic(fmt.Sprintf("unexpected jobType %v", response.JjobType))
		}
	}
	// uncomment to send the Example RPC to the coordinator.
	// CallExample()

}


func DelFileByReduceId(reduceId int, path string) error {
	// 创建正则表达式，X 是可变的指定数字
	pattern := fmt.Sprintf(`^mr-out-\d+-%d$`, reduceId)
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}

	// 读取当前目录中的文件
	files, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	// 遍历文件，查找匹配的文件
	for _, file := range files {
		if file.IsDir() {
			continue // 跳过目录
		}
		fileName := file.Name()
		if regex.MatchString(fileName) {
			// 匹配到了文件，删除它
			filePath := filepath.Join(path, file.Name())
			err := os.Remove(filePath)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func doMapTask(mapf func(string, string) []KeyValue,
	response HeartbeatResponse) error {

	filename := response.TtaskPtr.FileName
	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("cannot open %v", filename)
	}
	content, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalf("cannot read %v", filename)
	}
	file.Close()
	kva := mapf(filename, string(content))
	sort.Sort(ByKey(kva))

	filePrefix := "mr-out-" + strconv.Itoa(response.TtaskPtr.Id) + "-"

	// fmt.Println("filePrefix = ", filePrefix)
	// fmt.Println("id = ", response.TtaskId)

	key_group := map[string][]string{}

	for _, kv := range kva {
		key_group[kv.Key] = append(key_group[kv.Key], kv.Value)
	}

	// Map done, need to split Map result into nReduce files
	// ATOM file write

	filenameArray := make([]string, response.NnReduce)
	filePtr := make([]*os.File, response.NnReduce)
	for index := 0; index < response.NnReduce; index++ {
		filenameArray[index] = generateRandomString(20)
		_, err := os.Stat(filenameArray[index])
		if err != nil {
			if os.IsNotExist(err) {
				filePtr[index], _ = os.Create(filenameArray[index])
			} else {
				fmt.Println("(1)Error occured:", err)
			}
		} else {
			fmt.Println(filenameArray[index], " exists!\n", )
			// filePtr[index], _ = os.OpenFile(filenameArray[index], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			index--
			continue
		}
	}

	var ret error = nil
	for key, values := range key_group {
		reduceId := ihash(key) % response.NnReduce
		enc := json.NewEncoder(filePtr[reduceId])
		for _, value := range values {
			err := enc.Encode(&KeyValue{Key: key, Value: value})
			if err != nil {
				ret = err
				break
			}
		}
	}
	for index := 0; index < response.NnReduce; index++ {
		// rename those temp files
		reduceFilename := filePrefix + strconv.Itoa(index)
		_, err := os.Stat(reduceFilename)
		if err != nil {
			if os.IsNotExist(err) {
				os.Rename(filenameArray[index], reduceFilename)
			} else {
				fmt.Println("(2)Error occured:", err)
			}
		} else {
			fmt.Println(reduceFilename, " exists!\n")
			// os.Remove(reduceFilename)
			// os.Rename(filenameArray[index], reduceFilename)
		}
		
		filePtr[index].Close()
	}
	return ret

	
}

func reduceId2Filename(taskId int, path string) (fileList []*os.File, err error) {
	pattern := fmt.Sprintf(`^mr-out-\d+-%d$`, taskId)
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	// 读取当前目录中的文件
	files, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	// 遍历文件，查找匹配的文件
	for _, fileEntry := range files {
		if fileEntry.IsDir() {
			continue // 跳过目录
		}
		fileName := fileEntry.Name()
		if regex.MatchString(fileName) {
			filePath := filepath.Join(path, fileEntry.Name())
			file, err := os.Open(filePath)
			if err != nil {
				log.Fatalf("cannot open %v", filePath)
				for _, oFile := range fileList {
					oFile.Close()
				}
				return nil, err
			}
			fileList = append(fileList, file)
		}
	}
	return fileList, nil
}

func doReduceTask(reducef func(key string, values []string) string,
	response HeartbeatResponse) error {

	reduceId := response.TtaskId
	fileList, err := reduceId2Filename(reduceId, "./")
	mulKeyValue := map[string][]string{}

	if err != nil {
		return err
	}

	// collect all intermediate files

	for _, file := range fileList {
		dec := json.NewDecoder(file)
		for {
			var kv KeyValue
			if err := dec.Decode(&kv); err != nil {
				break
			}
			mulKeyValue[kv.Key] = append(mulKeyValue[kv.Key], kv.Value)
		}
		file.Close()
	}


	// get all keys and value and sort
	var keys []string
	for key := range mulKeyValue {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	reduceFilename := "mr-out-" + strconv.Itoa(response.TtaskId)
	// oname := generateRandomString(20)
	// ofile, err := os.Create(oname)
	// if err != nil {
	// 	return err
	// }
	var oname string
	var ofile *os.File
	
	for ;;{
		oname = generateRandomString(20)
		_, err := os.Stat(oname)
		if err != nil {
			if os.IsNotExist(err) {
				ofile, _ = os.Create(oname)
				break
			} else {
				fmt.Println("(1)Error occured:", err)
			}
		} else {
			fmt.Println(oname, " exists!\n", )
			continue
		}
	}
	

	defer ofile.Close()

	for _, key := range keys {
		output := reducef(key, mulKeyValue[key])
		_, err := fmt.Fprintf(ofile, "%v %v\n", key, output)
		if err != nil {
			return err
		}
	}
	_, err = os.Stat(reduceFilename)
	if err != nil {
		if os.IsNotExist(err) {
			os.Rename(oname, reduceFilename)
		} else {
			fmt.Println("(3)Error occured:", err)
		}
	} else {
		fmt.Println(reduceFilename, " exists!\n")
		// os.Remove(reduceFilename)
		// os.Rename(oname, reduceFilename)
	}


	DelFileByReduceId(reduceId, "./")

	return nil

	
}

//
// example function to show how to make an RPC call to the coordinator.
//
// the RPC argument and reply types are defined in rpc.go.
//
func CallExample() {

	// declare an argument structure.
	args := ExampleArgs{}

	// fill in the argument(s).
	args.X = 99

	// declare a reply structure.
	reply := ExampleReply{}

	// send the RPC request, wait for the reply.
	// the "Coordinator.Example" tells the
	// receiving server that we'd like to call
	// the Example() method of struct Coordinator.
	ok := call("Coordinator.Example", &args, &reply)
	if ok {
		// reply.Y should be 100.
		fmt.Printf("reply.Y %v\n", reply.Y)
	} else {
		fmt.Printf("call failed!\n")
	}
}

//
// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
//
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}