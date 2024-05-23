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

	Reqs sync.Map // clientID to sequence number

	lPrevs sync.Mutex
	Prevs  map[string]string // Map of clientID to previous value
}

func (server *KVServer) SendPrev(args *SendPrevArgs, reply *SendPrevReply) error {
	server.lPrevs.Lock()
	defer server.lPrevs.Unlock()
	server.Prevs = args.Prevs
	reply.Err = OK
	return nil
}

func (server *KVServer) Put(args *PutArgs, reply *PutReply) error {

	//TODO: REMOVE
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
		reply.PreviousValue = server.Prevs[args.ClientID]
		server.lPrevs.Unlock()
		reply.Err = OK
		return nil
	}

	server.Reqs.Store(args.ClientID, args.SeqNo)

	val := "" // default value

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
		server.Prevs[args.ClientID] = val
		server.lPrevs.Unlock()
		// server.mp[args.Key] = hStr
		server.mp.Store(args.Key, hStr)
	} else {
		// ordinary put
		// server.mp[args.Key] = args.Value
		server.mp.Store(args.Key, args.Value)
		server.lPrevs.Lock()
		server.Prevs[args.ClientID] = val
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

		ok := false
		for !ok || reply.Err != OK {
			// update view
			view, _ := server.monitorClnt.Ping(server.view.Viewnum)
			server.view = view

			ok = call(server.view.Backup, "KVServer.Put", args, &reply)
			server.backupExists = false
			DPrintf("Failed to forward key to backup\n")
		}
	}

	reply.Err = OK
	return nil
}

func (server *KVServer) Get(args *GetArgs, reply *GetReply) error {
	ok := false
	var view sysmonitor.View
	for !ok {
		view, ok = server.monitorClnt.Get()

	}
	server.view = view
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

// NOTE: This can be optimized by sending the entire map to the backup
func (server *KVServer) ForwardDataToBackup() {

	// This function is called when the primary server detects a new backup
	// and it should forward its data to the new backup.

	// Check if backup exists
	if server.view.Backup == "" {
		panic("[This should not happen] No backup server to forward data to.")
	}

	server.mp.Range(func(key, value interface{}) bool {
		view, _ := server.monitorClnt.Ping(server.view.Viewnum)
		server.view = view
		if server.view.Primary != server.id || server.view.Backup == "" {
			return false
		}

		args := &PutArgs{
			Key:    key.(string),
			Value:  value.(string),
			DoHash: false,
		}
		var reply PutReply
		ok := false

		for !ok || reply.Err != OK {
			ok = call(server.view.Backup, "KVServer.Put", args, &reply)
		}

		return true
	})

	//TODO: Sending Prev to backup
	// server.lPrevs.Lock()
	// args := &SendPrevArgs{
	// 	Prevs: server.Prevs,
	// }
	// server.lPrevs.Unlock()

	// var reply SendPrevReply

	// ok := false

	// for !ok || reply.Err != OK {
	// 	ok = call(server.view.Backup, "KVServer.SendPrev", args, &reply)
	// }

}

// ping the viewserver periodically.
func (server *KVServer) tick() {

	// This line will give an error initially as view and err are not used.
	view, _ := server.monitorClnt.Ping(server.view.Viewnum)

	//TODO: REMOVE
	// var view sysmonitor.View
	// var err error = fmt.Errorf("dummy error") // to make it like do while

	// for err != nil {
	// 	view, err = server.monitorClnt.Ping(server.view.Viewnum)
	// }

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
	server.view = sysmonitor.View{Primary: "", Backup: "", Viewnum: 0}
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
	server.Prevs = make(map[string]string)

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
