package kvservice

import (
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"net/rpc"
	"time"

	"github.com/iZarrios/replicated-key-value-service/pkg/sysmonitor"
)

var (
	// Commonly used in cryptography as it represents the maximum value that can be stored in a 256-bit integer.
	// 2**256 - 1
	MAX_RAND = new(big.Int).SetBytes([]byte("115792089237316195423570985008687907853269984665640564039457584007913129639935"))
)

type KVClient struct {
	monitorClnt *sysmonitor.Client

	// view provides information about which is primary, and which is backup.
	// Use updateView() to update this view when doing get and put as needed.
	view  sysmonitor.View
	id    string // should be generated to be a random string
	seqNo int
}

func MakeKVClient(monitorServer string) *KVClient {
	client := new(KVClient)
	client.monitorClnt = sysmonitor.MakeClient("", monitorServer)
	client.view = sysmonitor.View{} // An empty view.

	// rand.Reader is a global, shared instance of a cryptographically
	// secure random number generator.
	clientID, err := rand.Int(rand.Reader, MAX_RAND)
	if err != nil {
		panic("Could not generate random clientID")
	}

	client.id = clientID.String()
	client.seqNo = 1

	return client
}

// call() sends an RPC to the rpcname handler on server srv
// with arguments args, waits for the reply, and leaves the
// reply in reply. the reply argument should be a pointer
// to a reply structure.
//
// the return value is true if the server responded, and false
// if call() was not able to contact the server. in particular,
// the reply's contents are only valid if call() returned true.
//
// you should assume that call() will time out and return an
// error after a while if it doesn't get a reply from the server.
//
// please use call() to send all RPCs, in client.go and server.go.
// please don't change this function.
func call(srv string, rpcname string,
	args interface{}, reply interface{}) bool {
	c, errx := rpc.Dial("unix", srv)
	if errx != nil {
		return false
	}
	defer c.Close()

	err := c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}

// You can use this method to update the client's view when needed during get and put operations.
func (client *KVClient) updateView() {
	view, _ := client.monitorClnt.Get()
	client.view = view
}

// Fetch a key's value from the current primary via an RPC call.
// You can get the primary from the client's view.
// If the key was never set, "" is expected.
// This must keep trying until it gets a response.
func (client *KVClient) Get(key string) string {
	args := &GetArgs{Key: key}
	var reply GetReply

	var ok bool = false

	for !ok {
		client.updateView()
		ok = call(client.view.Primary, "KVServer.Get", &args, &reply)
	}

	return reply.Value
}

// This should tell the primary to update key's value via an RPC call.
// must keep trying until it succeeds.
// You can get the primary from the client's current view.
func (client *KVClient) PutAux(key string, value string, dohash bool) string {
	DPrintf("Sending Put with SeqNo = %v\n", client.seqNo)
	args := &PutArgs{
		Key:      key,
		Value:    value,
		DoHash:   dohash,
		ClientID: client.id,
		SeqNo:    client.seqNo,
	}

	var reply PutReply

	timeout := time.After(5 * time.Second)
	// ticker is used to periodically update the view.
	// This is useful when the primary has failed and the view has been updated.
	// This way, the client can keep trying to send the Put request to the new primary.
	ticker := time.NewTicker(10 * time.Microsecond) // FIXME: this should be a lot longer in practice.
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			log.Println("PutAux timed out")
			return ""
		case <-ticker.C:
			client.updateView()
			ok := call(client.view.Primary, "KVServer.Put", args, &reply)
			if ok {
				client.seqNo++
				return reply.PreviousValue
			}
		}
	}
}

// Both put and puthash rely on the auxiliary method PutAux. No modifications needed below.
func (client *KVClient) Put(key string, value string) {
	client.PutAux(key, value, false)
}

func (client *KVClient) PutHash(key string, value string) string {
	v := client.PutAux(key, value, true)
	return v
}
