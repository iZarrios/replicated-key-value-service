# Replicated Key-Value Service

This project implements a replicated key-value storage system using the Raft consensus algorithm. The goal is to ensure fault-tolerance and high availability of the service by replicating the data across multiple servers.

## Features

- Fault-tolerant key-value storage
- Data replication across multiple servers
- Raft consensus algorithm for managing replicas
- Simple client-server architecture

## Getting Started

### Prerequisites

- Go 1.16 or higher
- [Air](https://github.com/cosmtrek/air) (for live reloading)

### Installation

1. Clone the repository:

```sh
git clone https://github.com/iZarrios/replicated-key-value-service.git
cd replicated-key-value-service
```

2. Install dependencies:

```sh
go mod tidy
```

### Running the Service

1. Start the server:

```sh
go run server.go
```

2. In a separate terminal, run the client:

```sh
go run client.go
```

### Running Tests

To run the test suite:

```sh
go test ./...
```

## Project Structure

- `client.go`: Client-side code for interacting with the key-value service.
- `common.go`: Common utilities and definitions used across the project.
- `server.go`: Server-side code for the key-value service.
- `test_test.go`: Test cases for the project.
- `.air.toml`: Configuration file for the Air development tool.
- `go.mod`: Go module file specifying dependencies.
- `cmd/`: Directory containing command-line interface (CLI) related code.


## Requirements
* Clients send requests to the primary key/value server, and keep trying till their request gets a response.
* sysmonitor: This package has the code of the monitoring service. This has already been implemented.
* kvservice: This has the go files for the key/value server and client. This is where you will add your code. You can also add your code to common.go.
* main: This has an example_scenario.go file that illustrates an example of how the different components in the system work together. Reading this file in advance could give you an idea about how the different components of your implementation work together and the basic functionality expected.
* Acknowledgement. This assignment is an adapted version of an assignment used in MIT 6.824 and Princeton COS418 distributed systems courses.
* To avoid the split-brain problem, the key/value clients and servers rely on the simple monitoring service implemented in the sysmonitor package.
* sysmonitor provides a "view" (all clients and servers should use it)
* key/value server responds to clients only when it is the primary.
* The primary returned in a view must always be either the primary or the backup of the previous view. This excludes the initialization, when the service accepts any server as primary when it starts.
* no server is available = "" (empty string as an indicator)
* Each key/value server should send a Ping RPC once per PingInterval to the monitoring service. This is done through the following loop and in the tick() method of kvservice/server.go, which is called regularly as follows. This Ping RPC informs the monitoring service that the key/value server is still alive, and helps the key/value server decide if it needs to forward its data to a new detected backup server if any.
* Each key/value client also sends an RPC to the service to get the view as needed. This should not be done regularly. Clients can all updateView()
* case of backup failure (or backup promotion to be primary)
* the monitor service could make another available server to be the backup. When a new backup server is chosen, the primary should send all the needed information to the new backup, and then forward the incoming requests to the backup to ensure that the backup's key/value database is consistent with the primary.
* there could be a scenario where a primary server cannot contact the monitoring service, but it can contact the backup server.
* It is ok if the service works only with just one server, but your implementation should be able to incorporate a recovered or idle server (as backup), so that it can then tolerate another server failure.
* You will likely need to add customized RPC mechanisms to handle the interaction between the primary and backup key/value servers.
* Filtering Duplicate Requests: All operations should provide at-most-once semantics.
* Key/Value Server: Instead, servers Ping the service periodically (in kvservice/server.go's tick()) to learn about new views, as shown earlier.
* Key/Value Client: unless a Get or Put fail, or they are handling the first operation when the view is empty. In this case, it can request to update its view.
* Start by updating the tick() function in kvservice/server.go to process the view obtained from the Ping function. Once a server determines the current view, it can take note whether it is the primary, backup, or neither. If it is the primary, it can handle the client’s requests.
In the event that a primary server detects a new backup, it should forward its data to the new backup. The synchronization with the backup can be developed at a later stage.
* Implement Put and Get handlers in kvservice/server.go, storing keys and values in a map[string]string. For PutHash, compute the new value for a key using the hash function from common.go. The argument for the hash function should consist of the concatenation of the previous value for that key and the Value supplied by the client. Convert the result of the hash function, for use as the new value for the key, using strconv.Itoa(int(h)). If a key does not exist, PutHash can handle it in any way; one option might be to treat the previous value as blank (i.e., ""). Apart from the actual update to the key/value database, the code can handle PutHash() and Put() in the same manner (e.g., using PutArgs for both and sending the RPC through PutAux()).
* Modify the server Put handler so that the primary forwards updates to the backup. When a server becomes the backup in a new view, the primary should send it the complete key/value database.
* Update client.go so that clients continue retrying until they receive a response. Ensure that PutArgs and GetArgs (see common.go) include enough information for the key/value service to filter duplicates. Modify the key/value service to handle duplicates correctly. Note that the state to filter duplicates must be replicated along with the key/value state.
* Adjust client.go so that clients switch from a failed primary. If the current primary does not respond or does not recognize itself as the primary, have the client consult the monitoring service via the provided updateView() function and try again. Introduce a sleep for sysmonitor.PingInterval between retries to prevent excessive CPU usage.
* you'll likely need to create new RPCs to handle the transfer of the complete key/value database from the primary to a new backup. Assume that you can send the whole database in one RPC, e.g., include a map[string]string in the RPC arguments).
