package discovery

import (
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"

	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

type Nacos struct {
	Client naming_client.INamingClient
	Group  string
}

func (n Nacos) Watch(service string, update func([]string)) (func(), error) {
	// Keep the exact callback-bearing param for unsubscribe; a nil callback
	// would unsubscribe other snapshots watching the same service as well.
	var mu sync.Mutex
	closed, notified := false, false
	publish := func(instances []model.Instance) {
		addresses := []string{}
		for _, instance := range instances {
			if instance.Healthy && instance.Enable && instance.Weight > 0 && instance.Metadata["protocol"] == "grpc" {
				addresses = append(addresses, net.JoinHostPort(instance.Ip, strconv.FormatUint(instance.Port, 10)))
			}
		}
		update(addresses)
	}
	param := &vo.SubscribeParam{ServiceName: service, GroupName: n.Group}
	param.SubscribeCallback = func(instances []model.Instance, err error) {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		if err != nil {
			slog.Warn("Nacos discovery update failed", "service", service, "error", err)
			return
		}
		notified = true
		publish(instances)
	}
	if err := n.Client.Subscribe(param); err != nil {
		_ = n.Client.Unsubscribe(param)
		return nil, err
	}
	instances, err := n.Client.SelectInstances(vo.SelectInstancesParam{ServiceName: service, GroupName: n.Group, HealthyOnly: true})
	if err != nil {
		_ = n.Client.Unsubscribe(param)
		return nil, fmt.Errorf("discover %s: %w", service, err)
	}
	mu.Lock()
	if !notified {
		publish(instances)
	}
	mu.Unlock()
	return func() {
		mu.Lock()
		closed = true
		mu.Unlock()
		if err := n.Client.Unsubscribe(param); err != nil {
			slog.Warn("Nacos unsubscribe failed", "service", service, "error", err)
		}
	}, nil
}
