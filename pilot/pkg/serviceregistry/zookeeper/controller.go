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
	"path"
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

type ZkServiceInstance struct {
	ServiceInstance *model.ServiceInstance
	InstanceId      string
}

type Controller struct {
	client           *ZkClient
	services         map[string]*model.Service
	servicesList     []*model.Service
	serviceInstances map[string][]*ZkServiceInstance
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
	zkClient := NewZkClient(strings.Split(addr, ","), discoveryRootPath, nil)

	controller.client = zkClient
	controller.clusterId = clusterId
	controller.services = make(map[string]*model.Service)
	controller.servicesList = make([]*model.Service, 0)
	controller.serviceInstances = make(map[string][]*ZkServiceInstance)

	return &controller, nil
}

func (c *Controller) AppendServiceHandler(f func(*model.Service, model.Event)) {
	log.Printf("Zookeeper AppendServiceHandler")
}

func (c *Controller) AppendWorkloadHandler(f func(*model.WorkloadInstance, model.Event)) {
	log.Printf("Zookeeper AppendWorkloadHandler")
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

// 初始化
func (c *Controller) initCache() {
	services, err := c.getServices(c.client.DiscoveryRootPath)
	if err != nil {
		return
	}
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()
	if len(services) > 0 {
		for _, service := range services {

			// get instance list
			instances, err := c.getInstances(service)
			if err != nil {
				log.Printf("Failed to load service instance list, service: %s", service)
				continue
			}
			svc := c.genService(service)

			for _, instance := range instances {
				zkInstanceData, err := c.getInstanceData(service, instance)
				if err != nil {
					log.Printf("Failed to obtain service instance data, service: %s, instance: %s", service, instance)
					continue
				}
				serviceInstances := c.genZkInstanceData(service, instance, zkInstanceData, svc)
				c.serviceInstances[service] = serviceInstances
			}
			c.services[service] = svc
			c.servicesList = append(c.servicesList, svc)

		}
	}
}

func (c *Controller) genZkInstanceData(service string, instance string, zkInstanceData *ZkInstanceData, svc *model.Service) []*ZkServiceInstance {
	instanceLabels := make(map[string]string)
	instanceLabels["app"] = service
	tlsMode := model.GetTLSModeFromEndpointLabels(instanceLabels)
	svcPort := &model.Port{
		Name:     "http",
		Port:     80,
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
	serviceInstances = append(serviceInstances, &ZkServiceInstance{serviceInstance, instance})
	return serviceInstances
}

// 生成服务模型信息
func (c *Controller) genService(service string) *model.Service {
	hostname := fmt.Sprintf("%s.%s.svc.zk", service, model.IstioDefaultConfigNamespace)
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
			Labels: map[string]string{
				"app":     service,
				"service": service,
			},
			LabelSelectors: map[string]string{
				"app": service,
			},
		},
	}
	return svc
}

// 解析Hostname
func parseHostname(hostname host.Name) (name string, err error) {
	parts := strings.Split(string(hostname), ".")
	if len(parts) < 1 || parts[0] == "" {
		err = fmt.Errorf("missing service name from the service hostname %q", hostname)
		return
	}
	name = parts[0]
	return
}

// 获取服务列表, 并简单服务上下线
func (c *Controller) getServices(rootPath string) ([]string, error) {
	services, events, err := c.client.ChildrenW(rootPath)
	if err != nil {
		return nil, err
	}
	// 处理 根目录监听事件, 一般包含新服务上线, 或者旧服务下线
	go c.handleRootWatch(rootPath, events)
	return services, nil
}

// 获取服务的实例列表, 并监听实例变动事件
func (c *Controller) getInstances(service string) ([]string, error) {
	instancePath := path.Join(c.client.DiscoveryRootPath, service)
	instances, events, err := c.client.ChildrenW(instancePath)
	if err != nil {
		return nil, err
	}
	go c.handleServiceInstancesWatch(service, events)
	go c.handleServiceExistWatch(service)
	return instances, nil
}

// 获取实例内容, 并监听内容变化
func (c *Controller) getInstanceData(service string, instance string) (*ZkInstanceData, error) {
	instanceDataBytes, events, err := c.client.GetW(path.Join(c.client.DiscoveryRootPath, service, instance))
	if err != nil {
		return nil, err
	}
	go c.handleInstanceDataWatch(service, instance, events)
	go c.handleInstanceExistWatch(service, instance)
	zkInstanceData := &ZkInstanceData{}
	err = json.Unmarshal(instanceDataBytes, zkInstanceData)
	if err != nil {
		return nil, err
	}
	return zkInstanceData, nil
}

// 监听实例数据变动
func (c *Controller) handleInstanceDataWatch(service string, instance string, e <-chan zk.Event) {
	for {
		select {
		case event := <-e:
			{
				log.Printf("change v: %s, %s", event.Path, event.Type)
				switch event.Type {
				case zk.EventNodeChildrenChanged:
					// children changed
					log.Printf("instance data changed, %s, %s", service, instance)
					serviceObj := c.services[service]
					instanceDataBytes, _ := c.client.Get(path.Join(c.client.DiscoveryRootPath, service, instance))
					zkInstanceData := &ZkInstanceData{}
					err := json.Unmarshal(instanceDataBytes, zkInstanceData)
					if err != nil {
						log.Printf("Resolve instance data failed, %s, %s", service, instance)
						break
					}

					data := c.genZkInstanceData(service, instance, zkInstanceData, serviceObj)
					c.serviceInstances[service] = data
					break
				case zk.EventNodeCreated:
				case zk.EventNodeDeleted:
				case zk.EventNodeDataChanged:
				default:

				}
			}
		}

		_, events, _ := c.client.GetW(path.Join(c.client.DiscoveryRootPath, service, instance))
		e = events
	}
}

func (c *Controller) handleInstanceExistWatch(service string, instance string) {
	for {
		_, events, _ := c.client.ExistW(path.Join(c.client.DiscoveryRootPath, service, instance))
		select {
		case event := <-events:
			{
				switch event.Type {
				case zk.EventNodeDeleted:
					{
						log.Printf("instance deleted: %s, %s", service, instance)
						// 删除服务实例, 并取消监听
						instances := c.serviceInstances[service]
						for i, instan := range instances {
							if instan.InstanceId == instance {
								instances = append(instances[:i], instances[i+1:]...)
								break
							}
						}
						c.serviceInstances[service] = instances
						return
					}
				}
				break
			}

		}
	}
}

// 监听服务变动
func (c *Controller) handleRootWatch(rootPath string, e <-chan zk.Event) {
	for {
		select {
		case event := <-e:
			{
				log.Printf("change v: %s, %s", event.Path, event.Type)
				switch event.Type {
				case zk.EventNodeChildrenChanged:
					// children changed
					log.Printf("EventNodeChildrenChanged: %v", event)
					// 服务数量发生变化, 重新生成服务列表
					services, err := c.getServices(rootPath)
					if err != nil {
						log.Printf("%v", err)
					}
					// 找到变化的服务
					// 判断有没有新增的服务,新增服务, 并设置监听
					var svcAdded map[string]*model.Service
					for _, service := range services {
						_, ok := c.services[service]
						if !ok {
							log.Printf("new service dected, %s", service)
							svcAdded[service] = nil
						}
					}

					// 处理新增服务
					for service, _ := range svcAdded {
						instances, _ := c.getInstances(service)
						genService := c.genService(service)
						c.services[service] = genService
						c.servicesList = append(c.servicesList, genService)
						for _, instance := range instances {
							data, _ := c.getInstanceData(service, instance)
							instanceData := c.genZkInstanceData(service, instance, data, genService)
							c.serviceInstances[service] = instanceData
						}
					}

					break
				case zk.EventNodeCreated:
				case zk.EventNodeDeleted:
				case zk.EventNodeDataChanged:
				default:

				}
			}
		}

		_, events, _ := c.client.ChildrenW(rootPath)
		e = events
	}

}

// 监听服务实例变动
func (c *Controller) handleServiceInstancesWatch(service string, e <-chan zk.Event) {
	for {
		select {
		case event := <-e:
			{
				log.Printf("service instances change v: %s, %s", event.Path, event.Type)
				switch event.Type {
				case zk.EventNodeChildrenChanged:
					// children changed
					log.Printf("EventNodeChildrenChanged: service: %s, event: %v", service, event)
					// 获取缓存的所有实例
					// 获取zk所有实例
					instances := c.serviceInstances[service]
					serviceObj := c.services[service]
					// array to map
					var instancesMap = make(map[string]*ZkServiceInstance)
					for _, instance := range instances {
						instancesMap[instance.InstanceId] = instance
					}
					children, _ := c.client.Children(path.Join(c.client.DiscoveryRootPath, service))
					for _, child := range children {
						if _, ok := instancesMap[child]; !ok {
							// 新增的节点, 处理
							log.Printf("new instance dected, %s, %s", service, child)
							data, _ := c.getInstanceData(service, child)
							instances = c.genZkInstanceData(service, child, data, serviceObj)
							c.serviceInstances[service] = instances
							break
						}
					}
					break
				case zk.EventNodeCreated:
				case zk.EventNodeDeleted:
					{
						log.Printf("service deleted: %s", service)
						return
					}
				case zk.EventNodeDataChanged:
				default:

				}
			}
		}

		_, events, _ := c.client.ChildrenW(path.Join(c.client.DiscoveryRootPath, service))
		e = events
	}

}

func (c *Controller) handleServiceExistWatch(service string) {
	for {
		_, events, _ := c.client.ExistW(path.Join(c.client.DiscoveryRootPath, service))
		select {
		case event := <-events:
			{
				switch event.Type {
				case zk.EventNodeDeleted:
					{
						log.Printf("service deleted: %s", service)
						// 删除服务实例, 并取消监听
						delete(c.services, service)
						delete(c.serviceInstances, service)
						for i, svc := range c.servicesList {
							if svc.Attributes.Name == service {
								c.servicesList = append(c.servicesList[:i], c.servicesList[i+1:]...)
								break
							}
						}
						return
					}
				}
				break
			}

		}
	}
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
			if labels.HasSubsetOf(instance.ServiceInstance.Endpoint.Labels) /*&& portMatch(instance, servicePort)*/ {
				instances = append(instances, instance.ServiceInstance)
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
			addr := instance.ServiceInstance.Endpoint.Address
			if len(proxy.IPAddresses) > 0 {
				for _, ipAddress := range proxy.IPAddresses {
					if ipAddress == addr {
						out = append(out, instance.ServiceInstance)
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
			addr := instance.ServiceInstance.Endpoint.Address
			if len(proxy.IPAddresses) > 0 {
				for _, ipAddress := range proxy.IPAddresses {
					if ipAddress == addr {
						out = append(out, instance.ServiceInstance.Endpoint.Labels)
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
