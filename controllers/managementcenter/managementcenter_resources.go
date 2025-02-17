package managementcenter

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hazelcastv1alpha1 "github.com/hazelcast/hazelcast-platform-operator/api/v1alpha1"
	n "github.com/hazelcast/hazelcast-platform-operator/internal/naming"
	"github.com/hazelcast/hazelcast-platform-operator/internal/platform"
	"github.com/hazelcast/hazelcast-platform-operator/internal/util"
)

// Environment variables used for Management Center configuration
const (
	// mcLicenseKey License key for Management Center
	mcLicenseKey = "MC_LICENSE_KEY"
	// mcInitCmd init command for Management Center
	mcInitCmd = "MC_INIT_CMD"
	javaOpts  = "JAVA_OPTS"
)

func (r *ManagementCenterReconciler) executeFinalizer(ctx context.Context, mc *hazelcastv1alpha1.ManagementCenter) error {
	if !controllerutil.ContainsFinalizer(mc, n.Finalizer) {
		return nil
	}

	controllerutil.RemoveFinalizer(mc, n.Finalizer)
	err := r.Update(ctx, mc)
	if err != nil {
		return fmt.Errorf("failed to remove finalizer from custom resource: %w", err)
	}
	return nil
}

func (r *ManagementCenterReconciler) reconcileService(ctx context.Context, mc *hazelcastv1alpha1.ManagementCenter, logger logr.Logger) error {
	service := &corev1.Service{
		ObjectMeta: metadata(mc),
		Spec: corev1.ServiceSpec{
			Selector: labels(mc),
			Ports:    ports(),
		},
	}

	err := controllerutil.SetControllerReference(mc, service, r.Scheme)
	if err != nil {
		return fmt.Errorf("failed to set owner reference on Service: %w", err)
	}

	opResult, err := util.CreateOrUpdate(ctx, r.Client, service, func() error {
		service.Spec.Type = mc.Spec.ExternalConnectivity.ManagementCenterServiceType()
		return nil
	})
	if opResult != controllerutil.OperationResultNone {
		logger.Info("Operation result", "Service", mc.Name, "result", opResult)
	}
	return err
}

func (r *ManagementCenterReconciler) reconcileIngress(ctx context.Context, mc *hazelcastv1alpha1.ManagementCenter, logger logr.Logger) error {
	ingress := &networkingv1.Ingress{
		ObjectMeta: metadata(mc),
		Spec:       networkingv1.IngressSpec{},
	}

	if !mc.Spec.ExternalConnectivity.Ingress.IsEnabled() {
		err := r.Client.Delete(ctx, ingress)
		if err != nil && !kerrors.IsNotFound(err) {
			return err
		}
		if err == nil {
			logger.Info("Deleting ingress", "Ingress", mc.Name)
		}
		return nil
	}

	err := controllerutil.SetControllerReference(mc, ingress, r.Scheme)
	if err != nil {
		return fmt.Errorf("failed to set owner reference on Ingress: %w", err)
	}

	opResult, err := util.CreateOrUpdate(ctx, r.Client, ingress, func() error {
		ingress.Spec.IngressClassName = &mc.Spec.ExternalConnectivity.Ingress.IngressClassName
		ingress.ObjectMeta.Annotations = mc.Spec.ExternalConnectivity.Ingress.Annotations
		ingress.Spec.Rules = []networkingv1.IngressRule{
			{
				Host: mc.Spec.ExternalConnectivity.Ingress.Hostname,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{
							{
								Path:     "/",
								PathType: &[]networkingv1.PathType{networkingv1.PathTypePrefix}[0],
								Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: metadata(mc).Name,
										Port: networkingv1.ServiceBackendPort{
											Number: 8080,
										},
									},
								},
							},
						},
					},
				},
			},
		}
		return nil
	})
	if opResult != controllerutil.OperationResultNone {
		logger.Info("Operation result", "Ingress", mc.Name, "result", opResult)
	}
	return err
}

func metadata(mc *hazelcastv1alpha1.ManagementCenter) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      mc.Name,
		Namespace: mc.Namespace,
		Labels:    labels(mc),
	}
}
func labels(mc *hazelcastv1alpha1.ManagementCenter) map[string]string {
	return map[string]string{
		n.ApplicationNameLabel:         n.ManagementCenter,
		n.ApplicationInstanceNameLabel: mc.Name,
		n.ApplicationManagedByLabel:    n.OperatorName,
	}
}

func ports() []v1.ServicePort {
	return []corev1.ServicePort{
		{
			Name:       "http",
			Protocol:   corev1.ProtocolTCP,
			Port:       8080,
			TargetPort: intstr.FromString(n.Mancenter),
		},
		{
			Name:       "https",
			Protocol:   corev1.ProtocolTCP,
			Port:       443,
			TargetPort: intstr.FromString(n.Mancenter),
		},
	}
}

func (r *ManagementCenterReconciler) reconcileStatefulset(ctx context.Context, mc *hazelcastv1alpha1.ManagementCenter, logger logr.Logger) error {
	ls := labels(mc)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metadata(mc),
		Spec: appsv1.StatefulSetSpec{
			// Management Center StatefulSet size is always 1
			Replicas: &[]int32{1}[0],
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{{
						Name: n.ManagementCenter,
						Ports: []v1.ContainerPort{{
							ContainerPort: 8080,
							Name:          n.Mancenter,
							Protocol:      v1.ProtocolTCP,
						}},
						VolumeMounts: []corev1.VolumeMount{},
						LivenessProbe: &v1.Probe{
							ProbeHandler: v1.ProbeHandler{
								HTTPGet: &v1.HTTPGetAction{
									Path:   "/health",
									Port:   intstr.FromInt(8081),
									Scheme: corev1.URISchemeHTTP,
								},
							},
							InitialDelaySeconds: 10,
							TimeoutSeconds:      10,
							PeriodSeconds:       10,
							SuccessThreshold:    1,
							FailureThreshold:    10,
						},
						ReadinessProbe: &v1.Probe{
							ProbeHandler: v1.ProbeHandler{
								TCPSocket: &v1.TCPSocketAction{
									Port: intstr.FromInt(8080),
								},
							},
							InitialDelaySeconds: 10,
							TimeoutSeconds:      10,
							PeriodSeconds:       10,
							SuccessThreshold:    1,
							FailureThreshold:    10,
						},
						SecurityContext: containerSecurityContext(),
					}},
					SecurityContext: podSecurityContext(),
				},
			},
		},
	}

	err := controllerutil.SetControllerReference(mc, sts, r.Scheme)
	if err != nil {
		return fmt.Errorf("failed to set owner reference on StatefulSet: %w", err)
	}

	if mc.Spec.Persistence.IsEnabled() {
		if mc.Spec.Persistence.ExistingVolumeClaimName == "" {
			sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{persistentVolumeClaim(mc)}
		} else {
			sts.Spec.Template.Spec.Volumes = []v1.Volume{existingVolumeClaim(mc.Spec.Persistence.ExistingVolumeClaimName)}
		}
	} else {
		// Add emptyDir volume to make /data writable
		sts.Spec.Template.Spec.Volumes = []v1.Volume{emptyDirVolume(mc.Spec.Persistence.ExistingVolumeClaimName)}
	}
	// Add tmpDir to make /tmp writable
	sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes, tmpDir())
	sts.Spec.Template.Spec.Containers[0].VolumeMounts = []v1.VolumeMount{persistentVolumeMount(), tmpDirMount()}

	opResult, err := util.CreateOrUpdate(ctx, r.Client, sts, func() error {
		sts.Spec.Template.Spec.ImagePullSecrets = mc.Spec.ImagePullSecrets
		sts.Spec.Template.Spec.Containers[0].Image = mc.DockerImage()
		sts.Spec.Template.Spec.Containers[0].Env = env(mc)
		sts.Spec.Template.Spec.Containers[0].ImagePullPolicy = mc.Spec.ImagePullPolicy
		sts.Spec.Template.Spec.Containers[0].Resources = mc.Spec.Resources

		sts.Spec.Template.Spec.Affinity = mc.Spec.Scheduling.Affinity
		sts.Spec.Template.Spec.Tolerations = mc.Spec.Scheduling.Tolerations
		sts.Spec.Template.Spec.NodeSelector = mc.Spec.Scheduling.NodeSelector
		sts.Spec.Template.Spec.TopologySpreadConstraints = mc.Spec.Scheduling.TopologySpreadConstraints

		return nil
	})
	if opResult != controllerutil.OperationResultNone {
		logger.Info("Operation result", "Statefulset", mc.Name, "result", opResult)
	}
	return err
}

func podSecurityContext() *v1.PodSecurityContext {
	// Openshift assigns user and fsgroup ids itself
	if platform.GetType() == platform.OpenShift {
		return &v1.PodSecurityContext{
			RunAsNonRoot: pointer.Bool(true),
		}
	}

	return &v1.PodSecurityContext{
		RunAsNonRoot: pointer.Bool(true),
		// Do not have to give User and FSGroup IDs because MC image's default user is 1001 so kubelet
		// does not complain when RunAsNonRoot is true
		// To keep it consistent with Hazelcast, we are adding following
		RunAsUser: pointer.Int64(65534),
		FSGroup:   pointer.Int64(65534),
	}
}

func containerSecurityContext() *v1.SecurityContext {
	sec := &v1.SecurityContext{
		RunAsNonRoot:             pointer.Bool(true),
		Privileged:               pointer.Bool(false),
		ReadOnlyRootFilesystem:   pointer.Bool(true),
		AllowPrivilegeEscalation: pointer.Bool(false),
		Capabilities: &v1.Capabilities{
			Drop: []v1.Capability{"ALL"},
		},
	}

	return sec
}

func persistentVolumeMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      n.MancenterStorageName,
		MountPath: "/data",
	}
}

func tmpDirMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      n.TmpDirVolName,
		MountPath: "/tmp",
	}
}

func persistentVolumeClaim(mc *hazelcastv1alpha1.ManagementCenter) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      n.MancenterStorageName,
			Namespace: mc.Namespace,
			Labels:    labels(mc),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: mc.Spec.Persistence.StorageClass,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: *mc.Spec.Persistence.Size,
				},
			},
		},
	}
}

func existingVolumeClaim(claimName string) v1.Volume {
	return v1.Volume{
		Name: n.MancenterStorageName,
		VolumeSource: v1.VolumeSource{
			PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
				ClaimName: claimName,
			},
		},
	}
}

func emptyDirVolume(claimName string) v1.Volume {
	return v1.Volume{
		Name: n.MancenterStorageName,
		VolumeSource: v1.VolumeSource{
			EmptyDir: &v1.EmptyDirVolumeSource{},
		},
	}
}

func tmpDir() v1.Volume {
	return v1.Volume{
		Name: n.TmpDirVolName,
		VolumeSource: v1.VolumeSource{
			EmptyDir: &v1.EmptyDirVolumeSource{},
		},
	}
}

func env(mc *hazelcastv1alpha1.ManagementCenter) []v1.EnvVar {
	envs := []v1.EnvVar{{Name: mcInitCmd, Value: clusterAddCommand(mc)}}

	if mc.Spec.LicenseKeySecret != "" {
		envs = append(envs,
			v1.EnvVar{
				Name: mcLicenseKey,
				ValueFrom: &v1.EnvVarSource{
					SecretKeyRef: &v1.SecretKeySelector{
						LocalObjectReference: v1.LocalObjectReference{
							Name: mc.Spec.LicenseKeySecret,
						},
						Key: n.LicenseDataKey,
					},
				},
			},
			v1.EnvVar{
				Name: javaOpts,
				Value: fmt.Sprintf("-XX:+UseContainerSupport -Dhazelcast.mc.license=$(MC_LICENSE_KEY) -Dhazelcast.mc.healthCheck.enable=true"+
					" -Dhazelcast.mc.lock.skip=true -Dhazelcast.mc.tls.enabled=false -Dmancenter.ssl=false -Dhazelcast.mc.phone.home.enabled=%t", util.IsPhoneHomeEnabled()),
			},
		)
	} else {
		envs = append(envs,
			v1.EnvVar{
				Name: javaOpts,
				Value: fmt.Sprintf("-XX:+UseContainerSupport -Dhazelcast.mc.healthCheck.enable=true -Dhazelcast.mc.tls.enabled=false -Dmancenter.ssl=false"+
					" -Dhazelcast.mc.lock.skip=true -Dhazelcast.mc.phone.home.enabled=%t", util.IsPhoneHomeEnabled()),
			},
		)
	}
	return envs
}

func clusterAddCommand(mc *hazelcastv1alpha1.ManagementCenter) string {
	clusters := mc.Spec.HazelcastClusters
	strs := make([]string, len(clusters))
	for i, cluster := range clusters {
		strs[i] = fmt.Sprintf("./bin/mc-conf.sh cluster add --lenient=true -H /data -cn %s -ma %s", cluster.Name, cluster.Address)
	}
	return strings.Join(strs, " && ")
}
