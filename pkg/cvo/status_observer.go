package cvo

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"

	configv1 "github.com/openshift/api/config/v1"
	configinformersv1 "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
)

type clusterOperatorConditionObserver struct {
	configLister           configlistersv1.ClusterOperatorLister
	currentConditions      map[string][]configv1.ClusterOperatorStatusCondition
	currentConditionsMutex sync.Mutex
}

func newClusterOperatorConditionObserver(configInformer configinformersv1.ClusterOperatorInformer, recorder events.Recorder) factory.Controller {
	observer := &clusterOperatorConditionObserver{
		configLister: configInformer.Lister(),
	}
	// Create controller based on config informer and 30s manual resync period
	return factory.New().WithSync(observer.sync).ResyncEvery(30*time.Second).WithInformers(configInformer.Informer()).ToController("ClusterOperatorConditionObserver", recorder)
}

func (c *clusterOperatorConditionObserver) sync(_ context.Context, syncContext factory.SyncContext) error {
	// make sure we only sync once
	c.currentConditionsMutex.Lock()
	defer c.currentConditionsMutex.Unlock()

	operators, err := c.configLister.List(labels.Everything())
	if err != nil {
		runtime.HandleError(err)
		return err
	}
	currentConditions := c.currentConditions
	for i := range operators {
		current, newConditions := currentConditions[operators[i].Name], operators[i].Status.Conditions
		if !reflect.DeepEqual(current, newConditions) {
			c.processOperatorConditions(syncContext.Recorder(), operators[i].Name, current, newConditions)
		}
		c.currentConditions[operators[i].Name] = newConditions
	}

	return nil
}

func (c *clusterOperatorConditionObserver) processOperatorConditions(recorder events.Recorder, name string, current, observed []configv1.ClusterOperatorStatusCondition) {
	// we need semi-unique component suffix to prevent losing events
	componentSuffix := fmt.Sprintf("ClusterOperatorConditionObserver%s", strings.Title(name))
	for i := range current {
		new := resourcemerge.FindOperatorStatusCondition(observed, current[i].Type)
		messages := []string{}
		switch {
		case new.Status != current[i].Status:
			messages = append(messages, fmt.Sprintf("status changed from %q to %q", current[i].Status, new.Status))
		case new.Reason != current[i].Reason:
			messages = append(messages, fmt.Sprintf("reason changed from %q to %q", current[i].Reason, new.Status))
		case new.Message != current[i].Message:
			messages = append(messages, fmt.Sprintf("message changed from %q to %q", current[i].Message, new.Message))
		}
		if len(messages) == 0 {
			continue
		}

		// Operator "foo" condition "Degraded" reason changed from "Foo" to "Bar", status changed from True to False, etc...
		recorder.WithComponentSuffix(componentSuffix).Eventf("ConditionChanged", "Operator %q condition %q %s", name, new.Type, strings.Join(messages, ","))
	}
}
