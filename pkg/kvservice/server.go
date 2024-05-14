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
const Debug = 1

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
}

func (server *KVServer) Put(args *PutArgs, reply *PutReply) error {
	val := server.mp[args.Key]

	reply.PreviousValue = val

	if args.DoHash {
		key := hash(val + args.Value)
		keyStr := strconv.Itoa(int(key))

		server.mp[keyStr] = args.Value

	} else {
		// ordinary put
		server.mp[args.Key] = args.Value
	}

	// if server.view.Backup != "" {

	// 	// fmt.Println("Backup exists")
	// 	// forward the data to the backup
	// 	args := &PutArgs{
	// 		Key:    args.Key,
	// 		Value:  args.Value,
	// 		DoHash: args.DoHash,
	// 	}
	// 	var reply PutReply
	// 	ok := call(server.view.Backup, "KVServer.Put", args, &reply)
	// 	if !ok {
	// 		panic("Failed to forward key to backup")
	// 	} else {
	// 		panic("forwarded key to backup")
	// 	}
	// }

	return nil
}

func (server *KVServer) Get(args *GetArgs, reply *GetReply) error {
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
	if view.Backup != "" {
		server.backupExists = true
	}

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
			server.role = UNKOWN
		}
	}

	// If the current server is the primary,
	// and it does not already have a backup
	// and the view shows that there is a backup avaialbe
	// then we should synchornize with it

	// if server.role == PRIMARY && !server.backupExists && view.Backup != "" {
	// 	server.ForwardDataToBackup()
	// }

	// if server.role == BACKUP && view.Primary == "" {
	// 	fmt.Println("Primary is dead")
	// 	// if the primary is dead, then the backup should be the new primary
	// 	server.role = PRIMARY
	// 	server.backupExists = false
	// 	server.backup = ""
	// 	fmt.Printf("view: %v\n", view)
	// 	fmt.Printf("server.mp: %v\n", server.mp)

	// } else {
	// 	fmt.Printf("server.view: %#v\n", server.view)

	// }
	// server.printServerInfo()

}

// func (server *KVServer) printServerInfo() {
// 	roleMap := map[Role]string{
// 		0: "PRIMARY",
// 		1: "SECONDARY",
// 	}

// 	fmt.Printf("role=%v, id=%v, state=%v\n", roleMap[server.role], server.id, server.mp)
// }

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
