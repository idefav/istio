package zookeeper

import (
	"encoding/json"
	"fmt"
	"github.com/samuel/go-zookeeper/zk"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/serviceregistry"
	"istio.io/istio/pilot/pkg/serviceregistry/provider"
	"istio.io/istio/pkg/cluster"
	"istio.io/istio/pkg/config/host"
	"istio.io/istio/pkg/config/labels"
	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/spiffe"
	"log"
	"strings"
	"sync"
	"time"
)

/*
{
	"name": "user-service",
	"id": "cb6f4e79-2a21-4780-a05d-bc7e2b67ad3d",
	"address": "192.168.0.103",
	"port": 18081,
	"sslPort": null,
	"payload": {
		"@class": "org.springframework.cloud.zookeeper.discovery.ZookeeperInstance",
		"id": "user-service",
		"name": "user-service",
		"metadata": {
			"instance_status": "UP"
		}
	},
	"registrationTimeUTC": 1630757313240,
	"serviceType": "DYNAMIC",
	"uriSpec": {
		"parts": [{
			"value": "scheme",
			"variable": true
		}, {
			"value": "://",
			"variable": false
		}, {
			"value": "address",
			"variable": true
		}, {
			"value": ":",
			"variable": false
		}, {
			"value": "port",
			"variable": true
		}]
	}
}
*/
var _ serviceregistry.Instance = &Controller{}

type Controller struct {
	client           *ZkClient
	services         map[string]*model.Service
	servicesList     []*model.Service
	serviceInstances map[string][]*model.ServiceInstance
	clusterId        cluster.ID
	cacheMutex       sync.Mutex
}

type Options struct {
	ZookeeperAddr     string
	DiscoveryRootPath string
	ClusterID         cluster.ID
}

func NewController(addr string, discoveryRootPath string, clusterId cluster.ID) (*Controller, error) {
	controller := Controller{}
	zkClient := NewZkClient(strings.Split(addr, ","), discoveryRootPath, controller.zkWatchCallback)

	controller.client = zkClient
	controller.clusterId = clusterId
	controller.services = make(map[string]*model.Service)
	controller.servicesList = make([]*model.Service, 0)
	controller.serviceInstances = make(map[string][]*model.ServiceInstance)

	return &controller, nil
}

func (c *Controller) zkWatchCallback(event zk.Event) {
	log.Printf("zk change: %s, %s", event.Path, event.Type)
	switch event.Type {
	case zk.EventNodeChildrenChanged:
		break
	case zk.EventNodeCreated:
	case zk.EventNodeDeleted:
	case zk.EventNodeDataChanged:
	default:

	}
}

func (c *Controller) AppendServiceHandler(f func(*model.Service, model.Event)) {
	log.Printf("sss")
}

func (c *Controller) AppendWorkloadHandler(f func(*model.WorkloadInstance, model.Event)) {
	log.Printf("ttt")
}

func (c *Controller) Run(stop <-chan struct{}) {
	c.start(30*time.Second, stop)
}

func (c *Controller) start(time time.Duration, stop <-chan struct{}) {
	err := c.client.Connect(time)
	if err != nil {
		return
	}
	c.initCache()
	go func() {
		for {
			select {
			case <-stop:
				err := c.client.Close()
				if err != nil {
					return
				}
				return
			}
		}
	}()
}

func (c *Controller) initCache() {
	services, events, err := c.client.ChildrenW(c.client.DiscoveryRootPath)
	if err != nil {
		return
	}
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()
	go c.handleZkWatch(events)
	if len(services) > 0 {
		for _, service := range services {

			// get instance list
			instances, _ := c.client.Children(fmt.Sprintf("%s/%s", c.client.DiscoveryRootPath, service))

			svcPort := &model.Port{}
			hostname := fmt.Sprintf("%s.svc.zk", service)
			svc := &model.Service{
				Address: "0.0.0.0",
				Ports: model.PortList{&model.Port{
					Name:     "http",
					Port:     80,
					Protocol: protocol.HTTP,
				}},
				MeshExternal: false,
				ClusterLocal: model.HostVIPs{
					Hostname: host.Name(hostname),
					ClusterVIPs: cluster.AddressMap{
						Addresses: map[cluster.ID][]string{cluster.ID(provider.Zookeeper): {"0.0.0.0"}},
					},
				},
				Resolution: model.ClientSideLB,
				Attributes: model.ServiceAttributes{
					Name:            service,
					Namespace:       model.IstioDefaultConfigNamespace,
					ServiceRegistry: provider.Zookeeper,
				},
			}

			for _, instance := range instances {
				instanceDataBytes, _ := c.client.Get(fmt.Sprintf("%s/%s/%s", c.client.DiscoveryRootPath, service, instance))
				zkInstanceData := &ZkInstanceData{}
				err = json.Unmarshal([]byte(instanceDataBytes), zkInstanceData)
				if err != nil {
					continue
				}
				instanceLabels := make(map[string]string)
				tlsMode := model.GetTLSModeFromEndpointLabels(instanceLabels)
				svcPort = &model.Port{
					Name:     "http",
					Port:     zkInstanceData.Port,
					Protocol: protocol.HTTP,
				}
				svc.Ports = model.PortList{svcPort}
				serviceInstance := &model.ServiceInstance{
					Endpoint: &model.IstioEndpoint{
						Address:         zkInstanceData.Address,
						EndpointPort:    uint32(zkInstanceData.Port),
						ServicePortName: "http",
						Labels:          instanceLabels,
						TLSMode:         tlsMode,
						WorkloadName:    service,
						Namespace:       model.IstioDefaultConfigNamespace,
						Locality:        model.Locality{ClusterID: cluster.ID(provider.Zookeeper)},
					},
					ServicePort: svcPort,
					Service:     svc,
				}
				serviceInstances := c.serviceInstances[service]
				serviceInstances = append(serviceInstances, serviceInstance)
				c.serviceInstances[service] = serviceInstances
			}
			c.services[service] = svc
			c.servicesList = append(c.servicesList, svc)

		}
	}
}

func parseHostname(hostname host.Name) (name string, err error) {
	parts := strings.Split(string(hostname), ".")
	if len(parts) < 1 || parts[0] == "" {
		err = fmt.Errorf("missing service name from the service hostname %q", hostname)
		return
	}
	name = parts[0]
	return
}

func (c *Controller) handleZkWatch(e <-chan zk.Event) {
	event := <-e
	log.Printf("change v: %s, %s", event.Path, event.Type)
	switch event.Type {
	case zk.EventNodeChildrenChanged:
		// children changed

		break
	case zk.EventNodeCreated:
	case zk.EventNodeDeleted:
	case zk.EventNodeDataChanged:
	default:

	}
	//_, _, _ = c.client.GetW(c.client.DiscoveryRootPath)
}

func (c *Controller) HasSynced() bool {
	return true
}

func (c *Controller) Services() ([]*model.Service, error) {
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()
	return c.servicesList, nil
}

func (c *Controller) GetService(hostname host.Name) (*model.Service, error) {
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()
	name, err := parseHostname(hostname)
	if err != nil {
		log.Printf("parseHostname(%s) => error %v", hostname, err)
		return nil, err
	}
	if service, ok := c.services[name]; ok {
		return service, nil
	}
	return nil, nil

}

func (c *Controller) InstancesByPort(svc *model.Service, servicePort int, labels labels.Collection) []*model.ServiceInstance {
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()
	name, err := parseHostname(host.Name(svc.Attributes.Name))
	if err != nil {
		log.Printf("parseHostname(%s) => error %v", svc.Attributes.Name, err)
		return nil
	}
	if serviceInstances, ok := c.serviceInstances[name]; ok {
		var instances []*model.ServiceInstance
		for _, instance := range serviceInstances {
			if labels.HasSubsetOf(instance.Endpoint.Labels) /*&& portMatch(instance, servicePort)*/ {
				instances = append(instances, instance)
			}
		}

		return instances
	}
	return nil
}

func portMatch(instance *model.ServiceInstance, port int) bool {
	return port == 0 || port == instance.ServicePort.Port
}

func (c *Controller) GetProxyServiceInstances(proxy *model.Proxy) []*model.ServiceInstance {
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()
	out := make([]*model.ServiceInstance, 0)
	for _, instances := range c.serviceInstances {
		for _, instance := range instances {
			addr := instance.Endpoint.Address
			if len(proxy.IPAddresses) > 0 {
				for _, ipAddress := range proxy.IPAddresses {
					if ipAddress == addr {
						out = append(out, instance)
						break
					}
				}
			}
		}
	}
	return out
}

func (c *Controller) GetProxyWorkloadLabels(proxy *model.Proxy) labels.Collection {
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()
	out := make(labels.Collection, 0)
	for _, instances := range c.serviceInstances {
		for _, instance := range instances {
			addr := instance.Endpoint.Address
			if len(proxy.IPAddresses) > 0 {
				for _, ipAddress := range proxy.IPAddresses {
					if ipAddress == addr {
						out = append(out, instance.Endpoint.Labels)
						break
					}
				}
			}
		}
	}

	return out
}

func (c *Controller) GetIstioServiceAccounts(svc *model.Service, ports []int) []string {
	return []string{
		spiffe.MustGenSpiffeURI("default", "default"),
	}
}

func (c *Controller) NetworkGateways() []*model.NetworkGateway {
	return nil
}

func (c *Controller) Provider() provider.ID {
	return provider.Zookeeper
}

func (c *Controller) Cluster() cluster.ID {
	return c.clusterId
}
