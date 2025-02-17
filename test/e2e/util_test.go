package e2e

import (
	"context"
	"os"
	"reflect"
	"strings"
	. "time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func useExistingCluster() bool {
	return strings.ToLower(os.Getenv("USE_EXISTING_CLUSTER")) == "true"
}

func runningLocally() bool {
	return strings.ToLower(os.Getenv("RUN_MANAGER_LOCALLY")) == "true"
}

func GetControllerManagerName() string {
	return os.Getenv("DEPLOYMENT_NAME")
}

func GetSuiteName() string {
	if !ee {
		return "Operator Suite OS"
	}
	return "Operator Suite EE"
}

func getDeploymentReadyReplicas(ctx context.Context, name types.NamespacedName, deploy *appsv1.Deployment) (int32, error) {
	err := k8sClient.Get(ctx, name, deploy)
	if err != nil {
		if errors.IsNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	return deploy.Status.ReadyReplicas, nil
}

func assertDoesNotExist(name types.NamespacedName, obj client.Object) {
	Eventually(func() bool {
		err := k8sClient.Get(context.Background(), name, obj)
		if err == nil {
			return false
		}
		return errors.IsNotFound(err)
	}, 8*Minute, interval).Should(BeTrue())
}

func assertExists(name types.NamespacedName, obj client.Object) {
	Eventually(func() bool {
		err := k8sClient.Get(context.Background(), name, obj)
		return err == nil
	}, 20*Second, interval).Should(BeTrue())
}

func deletePVCs(lk types.NamespacedName) {
	pvcL := &corev1.PersistentVolumeClaimList{}
	Eventually(func() bool {
		err := k8sClient.List(context.Background(), pvcL, client.InNamespace(lk.Namespace))
		if err != nil {
			return false
		}
		for _, pvc := range pvcL.Items {
			if strings.Contains(pvc.Name, lk.Name) {
				err = k8sClient.Delete(context.Background(), &pvc, client.PropagationPolicy(metav1.DeletePropagationForeground))
				if err != nil {
					return false
				}
			}
		}
		return true
	}, 1*Minute, interval).Should(BeTrue())
}

func deletePods(lk types.NamespacedName) {
	By("deleting pods", func() {
		// Because pods get recreated by the StatefulSet controller, we are not using the eventually block here
		podL := &corev1.PodList{}
		err := k8sClient.List(context.Background(), podL, client.InNamespace(lk.Namespace))
		Expect(err).To(BeNil())
		for _, pod := range podL.Items {
			if strings.Contains(pod.Name, lk.Name) {
				err = k8sClient.Delete(context.Background(), &pod)
				Expect(err).To(BeNil())
			}
		}
	})
}

func DeleteAllOf(obj client.Object, objList client.ObjectList, ns string, labels map[string]string) {
	Expect(k8sClient.DeleteAllOf(
		context.Background(),
		obj,
		client.InNamespace(ns),
		client.MatchingLabels(labels),
		client.PropagationPolicy(metav1.DeletePropagationForeground),
	)).Should(Succeed())

	// do not wait if objList is nil
	if objList == nil {
		return
	}

	objListVal := reflect.ValueOf(objList)

	Eventually(func() int {
		err := k8sClient.List(context.Background(), objList,
			client.InNamespace(ns),
			client.MatchingLabels(labels))
		if err != nil {
			return -1
		}
		if objListVal.Kind() == reflect.Ptr || objListVal.Kind() == reflect.Interface {
			objListVal = objListVal.Elem()
		}
		items := objListVal.FieldByName("Items")
		return items.Len()
	}, 10*Minute, interval).Should(Equal(int(0)))
}
