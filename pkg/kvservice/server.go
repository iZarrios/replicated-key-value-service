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
	mp           sync.Map
	role         Role
	backupExists bool
	Reqs         sync.Map // Map of clientID to sequence number

	lPrevs sync.Mutex
	Prevs  map[string]map[int]string
}

func (server *KVServer) Put(args *PutArgs, reply *PutReply) error {

	{ // Debug
		if server.role == BACKUP {
			DPrintf("[BACKUP] Got Put request: %#v\n", args)
		} else if server.role == PRIMARY {
			DPrintf("[PRIMARY] Got Put request: %#v\n", args)
		}
	}

	sqno, ok := server.Reqs.Load(args.ClientID)

	SeqNo, _ := sqno.(int)

	if ok && SeqNo >= args.SeqNo && server.role == PRIMARY {
		DPrintf("[DUP] Put %#v\n", args)
		// Duplicate request
		server.lPrevs.Lock()
		reply.PreviousValue = server.Prevs[args.ClientID][args.SeqNo]
		server.lPrevs.Unlock()
		reply.Err = OK
		return nil
	}

	// server.Reqs[args.ClientID] = args.SeqNo
	server.Reqs.Store(args.ClientID, args.SeqNo)

	val := "" // default value

	// val, ok := server.mp[args.Key]
	valx, ok := server.mp.Load(args.Key)
	if !ok {
		val = ""
	}

	val, _ = valx.(string)

	reply.PreviousValue = val

	if args.DoHash {
		h := hash(val + args.Value)
		hStr := strconv.Itoa(int(h))
		server.lPrevs.Lock()
		if server.Prevs[args.ClientID] == nil {
			server.Prevs[args.ClientID] = make(map[int]string)
		}
		server.Prevs[args.ClientID][args.SeqNo] = val
		server.lPrevs.Unlock()
		// server.mp[args.Key] = hStr
		server.mp.Store(args.Key, hStr)
	} else {
		// ordinary put
		// server.mp[args.Key] = args.Value
		server.mp.Store(args.Key, args.Value)
		server.lPrevs.Lock()
		if server.Prevs[args.ClientID] == nil {
			server.Prevs[args.ClientID] = make(map[int]string)
		}
		server.Prevs[args.ClientID][args.SeqNo] = val
		server.lPrevs.Unlock()
	}

	if server.role == PRIMARY && server.view.Backup != "" {
		args := &PutArgs{
			Key:      args.Key,
			Value:    args.Value,
			DoHash:   args.DoHash,
			ClientID: args.ClientID,
			SeqNo:    args.SeqNo,
		}

		var reply PutReply

		DPrintf("Trying to send %#v to backup\n", args)

		ok := call(server.view.Backup, "KVServer.Put", args, &reply)
		if !ok || reply.Err != OK {
			server.backupExists = false
			DPrintf("Failed to forward key to backup")
		}
	}

	reply.Err = OK
	return nil
}

func (server *KVServer) Get(args *GetArgs, reply *GetReply) error {
	if server.role == BACKUP {
		DPrintf("[BACKUP] Got Get request: %#v\n", args)
		reply.Err = ErrWrongServer
		return nil
	}
	// reply.Value = server.mp[args.Key]
	valx, _ := server.mp.Load(args.Key)

	val, ok := valx.(string)
	if !ok {
		reply.Value = ""
		reply.Err = ErrNoKey
		return nil
	}

	reply.Value = val
	reply.Err = OK
	return nil
}

func (server *KVServer) SyncBackup(args *SyncBackupArgs, reply *PutReply) error {
	server.mp = sync.Map{}
	server.Prevs = make(map[string]map[int]string)
	server.Reqs = sync.Map{}

	for key, value := range args.Data {
		server.mp.Store(key, value)
	}

	for key, value := range args.Prevs {
		server.Prevs[key] = value
	}

	for key, value := range args.Reqs {
		server.Reqs.Store(key, value)
	}
	reply.Err = OK
	return nil
}

// NOTE: This can be optimized by sending the entire map to the backup
func (server *KVServer) ForwardDataToBackup() {
	// This function is called when the primary server detects a new backup
	// and it should forward its data to the new backup.

	// Check if backup exists
	if server.view.Backup == "" {
		log.Println("[This should not happen] No backup server to forward data to.")
		return
	}

	args := &SyncBackupArgs{
		Data:  make(map[string]string),
		Prevs: make(map[string]map[int]string),
		Reqs:  make(map[string]int),
	}

	server.mp.Range(func(key, value interface{}) bool {
		args.Data[key.(string)] = value.(string)
		return true
	})

	server.lPrevs.Lock()
	for key, value := range server.Prevs {
		args.Prevs[key] = value
	}
	server.lPrevs.Unlock()

	server.Reqs.Range(func(key, value interface{}) bool {
		args.Reqs[key.(string)] = value.(int)
		return true
	})

	var reply PutReply
	ok := call(server.view.Backup, "KVServer.SyncBackup", args, &reply)
	if !ok || reply.Err != OK {
		DPrintf("Failed to forward data to backup: %v\n", reply.Err)
	} else {
		DPrintf("Forwarded data to backup\n")
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
		DPrintf("Forwarding data to backup")
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

	// server.mp = make(map[string]string)
	// server.Reqs = make(map[string]int)
	server.Prevs = make(map[string]map[int]string)

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
