package utils

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/infracloudio/botkube/pkg/config"
	log "github.com/infracloudio/botkube/pkg/logging"
	appsV1 "k8s.io/api/apps/v1"
	batchV1 "k8s.io/api/batch/v1"
	coreV1 "k8s.io/api/core/v1"
	networkV1beta1 "k8s.io/api/networking/v1beta1"
	rbacV1 "k8s.io/api/rbac/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	// ResourceInformerMap is a map of resource name to resource Getter interface
	ResourceInformerMap map[string]cache.SharedIndexInformer
	// AllowedEventKindsMap is a map to filter valid event kinds
	AllowedEventKindsMap map[EventKind]bool
	// AllowedUpdateEventsMap is a map of resourceand namespace to updateconfig
	AllowedUpdateEventsMap map[KindNS]config.UpdateSetting
	// KubeClient is a global kubernetes client to communicate to apiserver
	KubeClient kubernetes.Interface
	// KubeInformerFactory is a global SharedInformerFactory object to watch resources
	KubeInformerFactory informers.SharedInformerFactory
)

// InitKubeClient creates K8s client from provided kubeconfig OR service account to interact with apiserver
func InitKubeClient() {
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		kubeconfigPath := os.Getenv("KUBECONFIG")
		if kubeconfigPath == "" {
			kubeconfigPath = os.Getenv("HOME") + "/.kube/config"
		}
		botkubeConf, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			log.Logger.Fatal(err)
		}
		KubeClient, err = kubernetes.NewForConfig(botkubeConf)
		if err != nil {
			log.Logger.Fatal(err)
		}
	} else {
		KubeClient, err = kubernetes.NewForConfig(kubeConfig)
		if err != nil {
			log.Logger.Fatal(err)
		}
	}
}

// EventKind used in AllowedEventKindsMap to filter event kinds
type EventKind struct {
	Resource  string
	Namespace string
	EventType config.EventType
}

// KindNS used in AllowedUpdateEventsMap
type KindNS struct {
	Resource  string
	Namespace string
}

// InitInformerMap initializes helper maps to filter events
func InitInformerMap() {
	botkubeConf, err := config.New()
	if err != nil {
		log.Logger.Fatal(fmt.Sprintf("Error in loading configuration. Error:%s", err.Error()))
	}

	// Get resync period
	rsyncTimeStr, ok := os.LookupEnv("INFORMERS_RESYNC_PERIOD")
	if !ok {
		rsyncTimeStr = "30"
	}
	rsyncTime, err := strconv.Atoi(rsyncTimeStr)
	if err != nil {
		log.Logger.Fatal("Error in reading INFORMERS_RESYNC_PERIOD env var.", err)
	}

	// Create shared informer factory
	KubeInformerFactory = informers.NewSharedInformerFactory(KubeClient, time.Duration(rsyncTime)*time.Minute)

	// Init maps
	ResourceInformerMap = make(map[string]cache.SharedIndexInformer)
	AllowedEventKindsMap = make(map[EventKind]bool)
	AllowedUpdateEventsMap = make(map[KindNS]config.UpdateSetting)

	// Informer map
	ResourceInformerMap["pod"] = KubeInformerFactory.Core().V1().Pods().Informer()
	ResourceInformerMap["node"] = KubeInformerFactory.Core().V1().Nodes().Informer()
	ResourceInformerMap["service"] = KubeInformerFactory.Core().V1().Services().Informer()
	ResourceInformerMap["namespace"] = KubeInformerFactory.Core().V1().Namespaces().Informer()
	ResourceInformerMap["replicationcontroller"] = KubeInformerFactory.Core().V1().ReplicationControllers().Informer()
	ResourceInformerMap["persistentvolume"] = KubeInformerFactory.Core().V1().PersistentVolumes().Informer()
	ResourceInformerMap["persistentvolumeClaim"] = KubeInformerFactory.Core().V1().PersistentVolumeClaims().Informer()
	ResourceInformerMap["secret"] = KubeInformerFactory.Core().V1().Secrets().Informer()
	ResourceInformerMap["configmap"] = KubeInformerFactory.Core().V1().ConfigMaps().Informer()

	ResourceInformerMap["deployment"] = KubeInformerFactory.Apps().V1().Deployments().Informer()
	ResourceInformerMap["daemonset"] = KubeInformerFactory.Apps().V1().DaemonSets().Informer()
	ResourceInformerMap["replicaset"] = KubeInformerFactory.Apps().V1().ReplicaSets().Informer()
	ResourceInformerMap["statefulset"] = KubeInformerFactory.Apps().V1().StatefulSets().Informer()

	ResourceInformerMap["ingress"] = KubeInformerFactory.Networking().V1beta1().Ingresses().Informer()

	ResourceInformerMap["job"] = KubeInformerFactory.Batch().V1().Jobs().Informer()

	ResourceInformerMap["role"] = KubeInformerFactory.Rbac().V1().Roles().Informer()
	ResourceInformerMap["rolebinding"] = KubeInformerFactory.Rbac().V1().RoleBindings().Informer()
	ResourceInformerMap["clusterrole"] = KubeInformerFactory.Rbac().V1().ClusterRoles().Informer()
	ResourceInformerMap["clusterrolebinding"] = KubeInformerFactory.Rbac().V1().RoleBindings().Informer()

	// Allowed event kinds map and Allowed Update Events Map
	for _, r := range botkubeConf.Resources {
		allEvents := false
		for _, e := range r.Events {
			if e == config.AllEvent {
				allEvents = true
				break
			}
			for _, ns := range r.Namespaces.Include {
				AllowedEventKindsMap[EventKind{Resource: r.Name, Namespace: ns, EventType: e}] = true
			}
			// AllowedUpdateEventsMap entry is created only for UpdateEvent
			if e == config.UpdateEvent {
				for _, ns := range r.Namespaces.Include {
					AllowedUpdateEventsMap[KindNS{Resource: r.Name, Namespace: ns}] = r.UpdateSetting
				}
			}
		}

		// For AllEvent type, add all events to map
		if allEvents {
			events := []config.EventType{config.CreateEvent, config.UpdateEvent, config.DeleteEvent, config.ErrorEvent}
			for _, ev := range events {
				for _, ns := range r.Namespaces.Include {
					AllowedEventKindsMap[EventKind{Resource: r.Name, Namespace: ns, EventType: ev}] = true
					AllowedUpdateEventsMap[KindNS{Resource: r.Name, Namespace: ns}] = r.UpdateSetting
				}
			}
		}
	}
	log.Logger.Infof("Allowed Events - %+v", AllowedEventKindsMap)
	log.Logger.Infof("Allowed UpdateEvents - %+v", AllowedUpdateEventsMap)
}

// GetObjectMetaData returns metadata of the given object
func GetObjectMetaData(obj interface{}) metaV1.ObjectMeta {

	var objectMeta metaV1.ObjectMeta

	switch object := obj.(type) {
	case *coreV1.Event:
		objectMeta = object.ObjectMeta
		// pass InvolvedObject`s annotations into Event`s annotations
		// for filtering event objects based on InvolvedObject`s annotations
		if len(objectMeta.Annotations) == 0 {
			objectMeta.Annotations = ExtractAnnotaions(object)
		} else {
			// Append InvolvedObject`s annotations to existing event object`s annotations map
			for key, value := range ExtractAnnotaions(object) {
				objectMeta.Annotations[key] = value
			}
		}

	case *coreV1.Pod:
		objectMeta = object.ObjectMeta
	case *coreV1.Node:
		objectMeta = object.ObjectMeta
	case *coreV1.Namespace:
		objectMeta = object.ObjectMeta
	case *coreV1.PersistentVolume:
		objectMeta = object.ObjectMeta
	case *coreV1.PersistentVolumeClaim:
		objectMeta = object.ObjectMeta
	case *coreV1.ReplicationController:
		objectMeta = object.ObjectMeta
	case *coreV1.Service:
		objectMeta = object.ObjectMeta
	case *coreV1.Secret:
		objectMeta = object.ObjectMeta
	case *coreV1.ConfigMap:
		objectMeta = object.ObjectMeta

	case *appsV1.DaemonSet:
		objectMeta = object.ObjectMeta
	case *appsV1.ReplicaSet:
		objectMeta = object.ObjectMeta
	case *appsV1.Deployment:
		objectMeta = object.ObjectMeta
	case *appsV1.StatefulSet:
		objectMeta = object.ObjectMeta

	case *networkV1beta1.Ingress:
		objectMeta = object.ObjectMeta

	case *batchV1.Job:
		objectMeta = object.ObjectMeta

	case *rbacV1.Role:
		objectMeta = object.ObjectMeta
	case *rbacV1.RoleBinding:
		objectMeta = object.ObjectMeta
	case *rbacV1.ClusterRole:
		objectMeta = object.ObjectMeta
	case *rbacV1.ClusterRoleBinding:
		objectMeta = object.ObjectMeta
	}
	return objectMeta
}

// GetObjectTypeMetaData returns typemetadata of the given object
func GetObjectTypeMetaData(obj interface{}) metaV1.TypeMeta {

	var typeMeta metaV1.TypeMeta

	switch object := obj.(type) {
	case *coreV1.Event:
		typeMeta = object.TypeMeta
	case *coreV1.Pod:
		typeMeta = object.TypeMeta
	case *coreV1.Node:
		typeMeta = object.TypeMeta
	case *coreV1.Namespace:
		typeMeta = object.TypeMeta
	case *coreV1.PersistentVolume:
		typeMeta = object.TypeMeta
	case *coreV1.PersistentVolumeClaim:
		typeMeta = object.TypeMeta
	case *coreV1.ReplicationController:
		typeMeta = object.TypeMeta
	case *coreV1.Service:
		typeMeta = object.TypeMeta
	case *coreV1.Secret:
		typeMeta = object.TypeMeta
	case *coreV1.ConfigMap:
		typeMeta = object.TypeMeta

	case *appsV1.DaemonSet:
		typeMeta = object.TypeMeta
	case *appsV1.ReplicaSet:
		typeMeta = object.TypeMeta
	case *appsV1.Deployment:
		typeMeta = object.TypeMeta
	case *appsV1.StatefulSet:
		typeMeta = object.TypeMeta

	case *networkV1beta1.Ingress:
		typeMeta = object.TypeMeta

	case *batchV1.Job:
		typeMeta = object.TypeMeta

	case *rbacV1.Role:
		typeMeta = object.TypeMeta
	case *rbacV1.RoleBinding:
		typeMeta = object.TypeMeta
	case *rbacV1.ClusterRole:
		typeMeta = object.TypeMeta
	case *rbacV1.ClusterRoleBinding:
		typeMeta = object.TypeMeta
	}
	return typeMeta
}

// DeleteDoubleWhiteSpace returns slice that removing whitespace from a arg slice
func DeleteDoubleWhiteSpace(slice []string) []string {
	result := []string{}
	for _, s := range slice {
		if len(s) != 0 {
			result = append(result, s)
		}
	}
	return result
}

// ExtractAnnotaions returns annotations of InvolvedObject for the given event
func ExtractAnnotaions(obj *coreV1.Event) map[string]string {

	switch obj.InvolvedObject.Kind {
	case "Pod":
		object, err := KubeClient.CoreV1().Pods(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "Node":
		object, err := KubeClient.CoreV1().Nodes().Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "Namespace":
		object, err := KubeClient.CoreV1().Namespaces().Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "PersistentVolume":
		object, err := KubeClient.CoreV1().PersistentVolumes().Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "PersistentVolumeClaim":
		object, err := KubeClient.CoreV1().PersistentVolumeClaims(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "ReplicationController":
		object, err := KubeClient.CoreV1().ReplicationControllers(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "Service":
		object, err := KubeClient.CoreV1().Services(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "Secret":
		object, err := KubeClient.CoreV1().Secrets(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "ConfigMap":
		object, err := KubeClient.CoreV1().ConfigMaps(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "DaemonSet":
		object, err := KubeClient.ExtensionsV1beta1().DaemonSets(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "Ingress":
		object, err := KubeClient.ExtensionsV1beta1().Ingresses(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)

	case "ReplicaSet":
		object, err := KubeClient.ExtensionsV1beta1().ReplicaSets(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "Deployment":
		object, err := KubeClient.ExtensionsV1beta1().Deployments(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "Job":
		object, err := KubeClient.BatchV1().Jobs(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "Role":
		object, err := KubeClient.RbacV1().Roles(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "RoleBinding":
		object, err := KubeClient.RbacV1().RoleBindings(obj.InvolvedObject.Namespace).Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "ClusterRole":
		object, err := KubeClient.RbacV1().ClusterRoles().Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	case "ClusterRoleBinding":
		object, err := KubeClient.RbacV1().ClusterRoleBindings().Get(obj.InvolvedObject.Name, metaV1.GetOptions{})
		if err == nil {
			return object.ObjectMeta.Annotations
		}
		log.Logger.Error(err)
	}

	return map[string]string{}
}

//GetClusterNameFromKubectlCmd this will return cluster name from kubectl command
func GetClusterNameFromKubectlCmd(cmd string) string {
	r, _ := regexp.Compile(`--cluster-name[=|' ']([^\s]*)`)
	//this gives 2 match with cluster name and without
	matchedArray := r.FindStringSubmatch(cmd)
	var s string
	if len(matchedArray) >= 2 {
		s = matchedArray[1]
	}
	return s
}
