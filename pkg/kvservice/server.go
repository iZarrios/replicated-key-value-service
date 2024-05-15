package kvservice

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/rpc"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/iZarrios/replicated-key-value-service/pkg/sysmonitor"
)

type Role int

const (
	PRIMARY Role = iota
	BACKUP
	UNKOWN
)

// Debugging
const Debug = 0

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug > 0 {
		n, err = fmt.Printf(format, a...)
	}
	return
}

type KVServer struct {
	l           net.Listener
	dead        bool // for testing
	unreliable  bool // for testing
	id          string
	monitorClnt *sysmonitor.Client
	view        sysmonitor.View
	done        sync.WaitGroup
	finish      chan interface{}

	// Add your declarations here.
	mp           map[string]string // this hold the state
	role         Role
	backupExists bool
	Reqs         map[string]int // Map of clientID to sequence number
}

func (server *KVServer) Put(args *PutArgs, reply *PutReply) error {

	if server.role == BACKUP {
		DPrintf("[BACKUP] Got Put request: %#v\n", args)
	} else if server.role == PRIMARY {
		DPrintf("[PRIMARY] Got Put request: %#v\n", args)
	}

	server.Reqs[args.ClientID]++

	// check if the clientID is in the Reqs map and if the sequence number is greater than the one in the map
	// if it is not, return an error
	// if it is, update the sequence number in the map and proceed with the Put operation
	// if the clientID is not in the map, add it to the map and proceed with the Put operation
	// seqNo, ok := server.Reqs[args.ClientID]
	// if !ok {
	// 	// if not found, add it to the map
	// 	server.Reqs[args.ClientID] = args.SeqNo
	// } else if (seqNo + 1) != args.SeqNo {
	// 	fmt.Printf("Expected sequence number %d, got %d\n", seqNo+1, args.SeqNo)
	// 	fmt.Printf("Found clientID %s in the map\n", args.ClientID)
	// 	reply.PreviousValue = server.mp[args.Key]
	// 	return nil
	// }

	DPrintf("Got Put request: %#v\n", args)
	val, ok := server.mp[args.Key]
	if !ok {
		val = ""
	}

	reply.PreviousValue = val

	if args.DoHash {
		h := hash(val + args.Value)
		keyStr := strconv.Itoa(int(h))

		server.mp[args.Key] = keyStr

	} else {
		// ordinary put
		server.mp[args.Key] = args.Value
	}

	if server.role == PRIMARY && server.view.Backup != "" {
		args := &PutArgs{
			Key:    args.Key,
			Value:  args.Value,
			DoHash: args.DoHash,
		}
		var reply PutReply

		DPrintf("Trying to send %#v to backup\n", args)

		ok := call(server.view.Backup, "KVServer.Put", args, &reply)
		if !ok {
			panic("Failed to forward key to backup")
		} else {
			DPrintf("[BACKUP] Forwarded key %s to backup\n", args.Key)
		}
	}

	return nil
}

func (server *KVServer) Get(args *GetArgs, reply *GetReply) error {
	if server.role == BACKUP {
		server.printServerInfo()
		fmt.Printf("server.view: %v\n", server.view)
		panic("Backup server should not receive Get requests")
	}

	reply.Value = server.mp[args.Key]
	return fmt.Errorf(string(reply.Err))
}

// NOTE: This can be optimized by sending the entire map to the backup
func (server *KVServer) ForwardDataToBackup() {
	// This function is called when the primary server detects a new backup
	// and it should forward its data to the new backup.

	// Check if backup exists
	if server.view.Backup == "" {
		log.Println("No backup server to forward data to.")
		return
	}

	for key, value := range server.mp {
		args := &PutArgs{
			Key:    key,
			Value:  value,
			DoHash: false,
		}
		var reply PutReply
		ok := call(server.view.Backup, "KVServer.Put", args, &reply)
		if !ok || reply.Err != "" {
			DPrintf("Failed to forward key %s to backup: %v\n", key, reply.Err)
		} else {
			DPrintf("Forwarded key %s to backup\n", key)
		}
	}
}

// ping the viewserver periodically.
func (server *KVServer) tick() {

	// This line will give an error initially as view and err are not used.
	view, err := server.monitorClnt.Ping(server.view.Viewnum)

	if err != nil {
		panic("The Sysmonitor is dead")
	}
	server.view = view

	switch server.id {
	case view.Primary:
		{
			server.role = PRIMARY
		}
	case view.Backup:
		{
			server.role = BACKUP
		}
	default:
		{
		}
	}

	// If the current server is the primary,
	// and it does not already have a backup
	// and the view shows that there is a backup avaialbe
	// then we should synchornize with it

	if view.Backup == "" {
		server.backupExists = false
	}

	if server.role == PRIMARY && !server.backupExists && view.Backup != "" {

		server.backupExists = true
		fmt.Println("Forwarding data to backup")
		server.ForwardDataToBackup()
	}

	server.printServerInfo()

}

func (server *KVServer) printServerInfo() {
	roleMap := map[Role]string{
		0: "PRIMARY",
		1: "SECONDARY",
	}
	DPrintf("role=%v, id=%v, state=%v\n", roleMap[server.role], server.id, server.mp)
}

// tell the server to shut itself down.
// please do not change this function.
func (server *KVServer) Kill() {
	server.dead = true
	server.l.Close()
}

func StartKVServer(monitorServer string, id string) *KVServer {
	server := new(KVServer)
	server.id = id
	server.monitorClnt = sysmonitor.MakeClient(id, monitorServer)
	server.view = sysmonitor.View{}
	server.finish = make(chan interface{})

	// Add your server initializations here
	// ==================================
	server.role = UNKOWN
	server.backupExists = false

	server.mp = make(map[string]string)
	server.Reqs = make(map[string]int)

	//====================================

	rpcs := rpc.NewServer()
	rpcs.Register(server)

	os.Remove(server.id)
	l, e := net.Listen("unix", server.id)
	if e != nil {
		log.Fatal("listen error: ", e)
	}
	server.l = l

	// please do not change any of the following code,
	// or do anything to subvert it.

	go func() {
		for server.dead == false {
			conn, err := server.l.Accept()
			if err == nil && server.dead == false {
				if server.unreliable && (rand.Int63()%1000) < 100 {
					// discard the request.
					conn.Close()
				} else if server.unreliable && (rand.Int63()%1000) < 200 {
					// process the request but force discard of reply.
					c1 := conn.(*net.UnixConn)
					f, _ := c1.File()
					err := syscall.Shutdown(int(f.Fd()), syscall.SHUT_WR)
					if err != nil {
						fmt.Printf("shutdown: %v\n", err)
					}
					server.done.Add(1)
					go func() {
						rpcs.ServeConn(conn)
						server.done.Done()
					}()
				} else {
					server.done.Add(1)
					go func() {
						rpcs.ServeConn(conn)
						server.done.Done()
					}()
				}
			} else if err == nil {
				conn.Close()
			}
			if err != nil && server.dead == false {
				fmt.Printf("KVServer(%v) accept: %v\n", id, err.Error())
				server.Kill()
			}
		}
		DPrintf("%s: wait until all request are done\n", server.id)
		server.done.Wait()
		// If you have an additional thread in your solution, you could
		// have it read to the finish channel to hear when to terminate.
		close(server.finish)
	}()

	server.done.Add(1)
	go func() {
		for server.dead == false {
			server.tick()
			time.Sleep(sysmonitor.PingInterval)
		}
		server.done.Done()
	}()

	return server
}
