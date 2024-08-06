package kvsrv

type PutAppendType = int
const (
	Puttype 	PutAppendType = iota
	Appendtype
)
// Put or Append
type PutAppendArgs struct {
	Key   string
	Value string
	// You'll have to add definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	PutOrAppend	PutAppendType

	// ClientId 		int
	// TransactionId 	int
}

type PutAppendReply struct {
	Value string

	// ClientId 		int
	// TransactionId 	int
}

type GetArgs struct {
	Key string
	// You'll have to add definitions here.

	// ClientId 		int
	// TransactionId 	int
}

type GetReply struct {
	Value string

	// ClientId 		int
	// TransactionId 	int
}
