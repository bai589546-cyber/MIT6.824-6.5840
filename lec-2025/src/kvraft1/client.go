package kvraft

import (
	"6.5840/kvsrv1/rpc"
	"6.5840/kvtest1"
	"6.5840/tester1"
	"time"
)


type Clerk struct {
	clnt    *tester.Clnt
	servers []string
	// You will have to modify this struct.
	lastLeader	int
}

func MakeClerk(clnt *tester.Clnt, servers []string) kvtest.IKVClerk {
	ck := &Clerk{clnt: clnt, servers: servers}
	// You'll have to add code here.
	ck.lastLeader = -1
	return ck
}

// Get fetches the current value and version for a key.  It returns
// ErrNoKey if the key does not exist. It keeps trying forever in the
// face of all other errors.
//
// You can send an RPC to server i with code like this:
// ok := ck.clnt.Call(ck.servers[i], "KVServer.Get", &args, &reply)
//
// The types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. Additionally, reply must be passed as a pointer.
func (ck *Clerk) Get(key string) (string, rpc.Tversion, rpc.Err) {

	// You will have to modify this function.
	args := rpc.GetArgs{
		key,
	}
	reply := rpc.GetReply{}
	i := ck.lastLeader
	if i == -1 {
		i = 0
	}
	for {
		ok := ck.clnt.Call(ck.servers[i], "KVServer.Get", &args, &reply)
		if ok && reply.Err != rpc.ErrWrongLeader{
			ck.lastLeader = i
			return reply.Value, reply.Version, reply.Err
		}
		i = (i + 1) % len(ck.servers)
		if i == 0 || !ok {
			time.Sleep(50 * time.Millisecond) // 不是 Leader 就睡一会
		}
	}
	return reply.Value, reply.Version, reply.Err
}

// Put updates key with value only if the version in the
// request matches the version of the key at the server.  If the
// versions numbers don't match, the server should return
// ErrVersion.  If Put receives an ErrVersion on its first RPC, Put
// should return ErrVersion, since the Put was definitely not
// performed at the server. If the server returns ErrVersion on a
// resend RPC, then Put must return ErrMaybe to the application, since
// its earlier RPC might have been processed by the server successfully
// but the response was lost, and the the Clerk doesn't know if
// the Put was performed or not.
//
// You can send an RPC to server i with code like this:
// ok := ck.clnt.Call(ck.servers[i], "KVServer.Put", &args, &reply)
//
// The types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. Additionally, reply must be passed as a pointer.
func (ck *Clerk) Put(key string, value string, version rpc.Tversion) rpc.Err {
	// You will have to modify this function.
	// You will have to modify this function.
	args := rpc.PutArgs{
		key,
		value,
		version,
	}
	reply := rpc.PutReply{}
	i := ck.lastLeader
	if i == -1 {
		i = 0
	}

	isResend := false
	for {
		ok := ck.clnt.Call(ck.servers[i], "KVServer.Put", &args, &reply)
		if ok && reply.Err != rpc.ErrWrongLeader{
			ck.lastLeader = i
			if isResend && reply.Err == rpc.ErrVersion {
				reply.Err = rpc.ErrMaybe
			}
			return reply.Err
		}
		i = (i + 1) % len(ck.servers)
		isResend = true
		if i == 0 || !ok {
			time.Sleep(50 * time.Millisecond) // 不是 Leader 就睡一会
		}
	}
	return reply.Err
}
