package kvservice

import (
	"crypto/rand"
	"hash/fnv"
	"math/big"
)

const (
	OK             = "OK"
	ErrNoKey       = "ErrNoKey"
	ErrWrongServer = "ErrWrongServer"
)

type Err string

type PutArgs struct {
	Key    string
	Value  string
	DoHash bool // For PutHash
	// You'll have to add definitions here.

	ClientID string // the clientID of the client that sent the request
	SeqNo    int    // the sequence number of the request associated with the clientID

	// Field names should start with capital letters for RPC to work.
}

type PutReply struct {
	Err           Err
	PreviousValue string // For PutHash
}

type GetArgs struct {
	Key string
	// You'll have to add definitions here.
}

type GetReply struct {
	Err   Err
	Value string
}

// Add your RPC definitions here.
// ======================================
type SendPrevArgs struct {
	Prevs map[string]string
}

type SendPrevReply struct {
	Err Err
}

// ======================================

func hash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}
