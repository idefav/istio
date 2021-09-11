package zookeeper

import (
	"github.com/samuel/go-zookeeper/zk"
	"time"
)

type ZkClient struct {
	ConnectString     []string
	ZkConn            *zk.Conn
	WatchCallback     zk.EventCallback
	DiscoveryRootPath string
}

// NewZkClient new zk client
func NewZkClient(connectString []string, discoveryRootPath string, watchCallback func(event zk.Event)) *ZkClient {
	client := ZkClient{
		ConnectString:     connectString[:],
		ZkConn:            nil,
		WatchCallback:     watchCallback,
		DiscoveryRootPath: discoveryRootPath,
	}
	return &client
}

// Close Zookeeper Client
func (client *ZkClient) Close() error {
	if client.ZkConn != nil {
		client.ZkConn.Close()
	}
	return nil
}

func (client *ZkClient) Connect(sessionTimeout time.Duration) error {
	// Determine whether the connection exists
	if client.ZkConn != nil {
		client.Close()
	}
	eventCallbackOption := zk.WithEventCallback(client.WatchCallback)
	// Create connection
	c, _, err := zk.Connect(client.ConnectString, sessionTimeout, eventCallbackOption)
	if err != nil {
		return err
	}
	// Assign value to structure variable, reuse the connection
	client.ZkConn = c
	return nil
}

// Create Zookeeper Node
func (client *ZkClient) Create(path string, data []byte) error {
	_, err := client.ZkConn.Create(path, data, 0, nil)
	return err
}

// Determine whether znode exists
func (client *ZkClient) Exist(path string) (bool, error) {
	isExist, _, err := client.ZkConn.Exists(path)
	return isExist, err
}

func (client *ZkClient) ExistW(path string) (bool, <-chan zk.Event, error) {
	isExist, _, events, err := client.ZkConn.ExistsW(path)
	return isExist, events, err
}

func (client *ZkClient) Children(path string) ([]string, error) {
	children, _, err := client.ZkConn.Children(path)
	return children, err
}

func (client *ZkClient) ChildrenW(path string) ([]string, <-chan zk.Event, error) {
	children, _, events, err := client.ZkConn.ChildrenW(path)
	return children, events, err
}

// Set znode value
func (client *ZkClient) Set(path string, data []byte, version int32) error {
	_, err := client.ZkConn.Set(path, data, version)
	return err
}

// Get the value of znode
func (client *ZkClient) Get(path string) ([]byte, error) {
	data, _, err := client.ZkConn.Get(path)
	return data, err
}

func (client *ZkClient) GetW(path string) ([]byte, <-chan zk.Event, error) {
	data, _, events, err := client.ZkConn.GetW(path)
	return data, events, err
}

// Delete znode
func (client *ZkClient) delete(path string) error {
	err := client.ZkConn.Delete(path, -1)
	return err
}
