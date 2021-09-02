package controllers

import (
	"context"
	"github.com/go-logr/logr"
	"github.com/hazelcast/hazelcast-enterprise-operator/api/v1alpha1"
	"github.com/hazelcast/hazelcast-go-client"
	"github.com/hazelcast/hazelcast-go-client/cluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"time"
)

type HazelcastClient struct {
	Client              *hazelcast.Client
	NamespacedName      types.NamespacedName
	Log                 logr.Logger
	MemberMap           map[string]bool
	Ticker              *time.Ticker
	memberEventsChannel chan event.GenericEvent
}

func (c HazelcastClient) Shutdown(ctx context.Context) error {
	err := c.Client.Shutdown(ctx)
	return err
}

func NewHazelcastClient(l logr.Logger, n types.NamespacedName, channel chan event.GenericEvent) HazelcastClient {
	return HazelcastClient{
		NamespacedName:      n,
		Log:                 l,
		MemberMap:           make(map[string]bool),
		memberEventsChannel: channel,
	}
}

func (c HazelcastClient) start(ctx context.Context, config hazelcast.Config) error {
	hzClient, err := hazelcast.StartNewClientWithConfig(ctx, config)
	if err != nil {
		return err
	}
	c.Client = hzClient
	return nil
}

func getStatusUpdateListener(hzClient HazelcastClient) func(cluster.MembershipStateChanged) {
	return func(changed cluster.MembershipStateChanged) {
		if changed.State == cluster.MembershipStateAdded {
			hzClient.MemberMap[changed.Member.String()] = true
		} else if changed.State == cluster.MembershipStateRemoved {
			delete(hzClient.MemberMap, changed.Member.String())
		}
		hzClient.triggerReconcile()
	}
}

func (hzClient HazelcastClient) triggerReconcile() {
	hzClient.memberEventsChannel <- event.GenericEvent{
		Object: &v1alpha1.Hazelcast{ObjectMeta: metav1.ObjectMeta{
			Namespace: hzClient.NamespacedName.Namespace,
			Name:      hzClient.NamespacedName.Name,
		}}}
}
