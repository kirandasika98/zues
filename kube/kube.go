package kube

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"zues/config"
	pubsub "zues/dispatch"
	"zues/util"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/gorilla/websocket"
	"github.com/kataras/golog"

	"os"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Sessionv1 is a struct to represent the global K8s session
type Sessionv1 struct {
	clientSet    *kubernetes.Clientset
	apiCalls     uint64
	hasStarted   bool
	addedChan    chan *apiv1.Pod
	deletedChan  chan *apiv1.Pod
	modifiedChan chan *apiv1.Pod
	errorChan    chan *apiv1.Pod
}

const (
	// LogPollingFrequency is the polling duration in seconds
	LogPollingFrequency = 2
)

var (
	// Session is  used as a global variables throughout the program
	Session *Sessionv1
)

// New Create a new kuberentes session
func New() (*Sessionv1, error) {
	var config *rest.Config
	var err error
	if os.Getenv("IN_CLUSTER") == "true" {
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("error while creating a kube session")
		}
	} else {
		var kubeconfig string
		kubeconfig = "./kubeconfig"
		// use the current context in kubeconfig
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("error while creating a local kube session. file not available")
		}
	}
	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())

	}
	// Create a new session
	var s Sessionv1
	s.apiCalls = 0
	s.clientSet = clientset
	s.hasStarted = false
	s.addedChan = make(chan *apiv1.Pod)
	s.modifiedChan = make(chan *apiv1.Pod)
	s.deletedChan = make(chan *apiv1.Pod)
	s.errorChan = make(chan *apiv1.Pod)

	// This go routine handles differnt types of events sent by the k8s server
	// Will be monitoring all the different events behind the scenes
	go s.handlePodEvent()

	return &s, nil
}

// Clientset returns the current instance of the kubernetes client config
func (s *Sessionv1) Clientset() *kubernetes.Clientset {
	return s.clientSet
}

// CreatePod create a pod
func (s *Sessionv1) CreatePod(serviceName, namespace string,
	labels map[string]string, container apiv1.Container) (*apiv1.Pod, error) {
	podName := strings.ToLower(serviceName + "-" + util.RandomString(5) + "-" + util.RandomString(5))
	pod, err := s.clientSet.CoreV1().Pods(namespace).Create(&apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: apiv1.PodSpec{
			Containers: []apiv1.Container{
				container,
			},
			ImagePullSecrets: []apiv1.LocalObjectReference{
				{
					// TODO: remove this and make this a environment variable
					Name: "secqat",
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	return pod, nil
}

// GetPod gets the specific pod in a namespace
func (s *Sessionv1) GetPod(podName, namespace string) (*apiv1.Pod, error) {
	if len(namespace) == 0 {
		namespace = "default"
	}
	pod, err := s.clientSet.CoreV1().Pods(namespace).Get(podName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	s.apiCalls++
	return pod, nil
}

// DeletePod deletes the pod in a specific namespace
func (s *Sessionv1) DeletePod(podName, namespace string) error {
	if len(namespace) == 0 {
		namespace = "default"
	}
	err := s.clientSet.CoreV1().Pods(namespace).Delete(podName, &metav1.DeleteOptions{})
	s.apiCalls++
	if err != nil {
		return err
	}
	return nil
}

// ListPods lists all the pods in a given namespace
func (s *Sessionv1) ListPods(namespace string) (*apiv1.PodList, error) {
	if len(namespace) == 0 {
		namespace = "default"
	}
	podList, err := s.clientSet.CoreV1().Pods(namespace).List(metav1.ListOptions{})
	s.apiCalls++
	if err != nil {
		return nil, err
	}

	return podList, err
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE")
}

// CreateContainer creates a K8s container with the given parameters
func CreateContainer(name, image string, port int32) apiv1.Container {
	return apiv1.Container{
		Name:            name,
		Image:           image,
		ImagePullPolicy: apiv1.PullAlways,
		Ports: []apiv1.ContainerPort{
			{
				Name:          "http", // Currently only supporting HTTP servers
				Protocol:      apiv1.ProtocolTCP,
				ContainerPort: port,
			},
		},
	}
}

// WatchPodEvents watches events on Pods
func (s *Sessionv1) WatchPodEvents(startWatchChan <-chan struct{}) {
	// Waiting for the server to startup
	<-startWatchChan
	golog.Println("Watching Pod Events...")
	watcher, err := s.clientSet.CoreV1().Pods("sysz").Watch(metav1.ListOptions{})
	if err != nil {
		golog.Errorf("Error occured: %s", err.Error())
	}
	for event := range watcher.ResultChan() {
		pod, ok := event.Object.(*apiv1.Pod)
		if !ok {
			golog.Error("Failed to convert to a type of apiv1.Pod")
			continue
		}
		switch event.Type {
		case watch.Added:
			s.addedChan <- pod
		case watch.Modified:
			s.modifiedChan <- pod
		case watch.Error:
			s.errorChan <- pod
		case watch.Deleted:
			s.deletedChan <- pod
		}
	}

	close(s.addedChan)
	close(s.modifiedChan)
	close(s.errorChan)
	close(s.deletedChan)
}

func (s *Sessionv1) handlePodEvent() {
	for {
		select {
		case pod := <-s.addedChan:
			if pod != nil {
				golog.Infof("ADDED Pod %s", pod.ObjectMeta.Name)
			}
		case pod := <-s.modifiedChan:
			if pod != nil {
				for _, c := range pod.Status.ContainerStatuses {
					// Check if the container is terminated
					if c.State.Terminated != nil {
						golog.Infof("TERMINATION Pod: %s Status: %s Restarts: %d", pod.ObjectMeta.Name, c.State.Terminated.Reason, c.RestartCount)
					} else if c.State.Waiting != nil {
						golog.Infof("WAITING Pod %s Status: %s Restarts: %d", pod.ObjectMeta.Name, c.State.Waiting.Reason, c.RestartCount)
					} else if c.State.Running != nil {
						golog.Infof("RUNNING Pod %s StartedAt: %s Restarts: %d", pod.ObjectMeta.Name, c.State.Running.StartedAt, c.RestartCount)
					}
				}
				jobID, err := config.MatchJobIDWithPod(pod.ObjectMeta.Name)
				if err != nil {
					golog.Errorf("Could not find JobID with Pod %s", pod.ObjectMeta.Name)
					continue
				}
				config.JobPodsMap[jobID] = pod
				// Delete the Pod if it has crossed the number of build errors
				if deleteFlag, err := s.shouldTerminate(jobID); err != nil {
					golog.Errorf(err.Error())
				} else if deleteFlag {
					s.DeletePod(pod.ObjectMeta.Name, pod.ObjectMeta.Namespace)
				}
			}
		case pod := <-s.errorChan:
			if pod != nil {
				golog.Errorf("Error Pod %s", pod.ObjectMeta.Name)
			}
		case pod := <-s.deletedChan:
			if pod != nil {
				golog.Infof("Pod %s DELETED", pod.ObjectMeta.Name)
			}
		}
	}
}

func (s *Sessionv1) shouldTerminate(jobID string) (bool, error) {
	if jobID == "" {
		return false, fmt.Errorf("Please provide a jobID")
	}
	job, ok := config.CurrentJobs[jobID]
	if !ok {
		return false, fmt.Errorf("No job with id %s", jobID)
	}
	pod := config.JobPodsMap[jobID]
	for _, c := range pod.Status.ContainerStatuses {
		if c.RestartCount >= job.Spec.MaxBuildErrors {
			return true, nil
		}
	}
	return false, nil
}

// StreamLogsToChannel stream the logs of all pods under a service to a give channel
func (s *Sessionv1) StreamLogsToChannel(serviceName, channelName string, wsConn *websocket.Conn) {
	// Setting up pubsub dependencies
	c, err := pubsub.GetChannel(channelName)
	if c == nil || err != nil {
		// Create a new channel
		c, err = pubsub.NewChannel(channelName)
		if err != nil {
			golog.Errorf("Error while creating channel: %s", err.Error())
		}
		isAdded, err := c.AddListener(wsConn)
		if err != nil || !isAdded {
			golog.Errorf("Error adding listener: %s", err.Error())
		}
	}
	// Temp stategy: look for selector service name and for each pod in that service
	// and query the logs
	// TODO: change it to the active namespace within the system
	tempNamespace := "sysz"
	svc, err := s.Clientset().CoreV1().Services(tempNamespace).Get(serviceName, metav1.GetOptions{})
	if err != nil || svc == nil {
		return
	}
	appLabels := labels.Set(svc.Spec.Selector)
	pods, err := s.Clientset().CoreV1().Pods(tempNamespace).List(metav1.ListOptions{LabelSelector: labels.FormatLabels(appLabels)})
	for _, pod := range pods.Items {
		logBytes := s.getLogsForPod(tempNamespace, pod.ObjectMeta.Name).Bytes()
		if len(logBytes) > 1024 {
			// Chunk the log output so that we don't have write timeouts to the websocket connections
			offset := 0
			offsetMax := 1024
			for offset <= len(logBytes) {
				c.BroadcastBinary(logBytes[offset:offsetMax])
				offset += 1024
				offsetMax += 1024
			}
		} else {
			// Dump the entire log to the connection
		}
	}
}

// getLogsForPod is a helper function that queries the kubernetes api server for pod logs
func (s *Sessionv1) getLogsForPod(namespace, podName string) *bytes.Buffer {
	logReq := s.clientSet.RESTClient().Get().RequestURI("/api/v1/namespaces/" + namespace + "/pods/" + podName + "/log")
	readerCloser, err := logReq.Stream()
	if err != nil {
		golog.Errorf(err.Error())
		return nil
	}
	defer readerCloser.Close()
	logBuff := bytes.NewBuffer([]byte(""))
	io.Copy(logBuff, readerCloser)
	return logBuff
}

// Stop is a required interface method needed to
// be implmented by every service in suture
func (s *Sessionv1) Stop() {
	s = nil
	return
}

// Serve is a required interface method needed to
// be implmented by every service in suture
func (s *Sessionv1) Serve() {
	start_watch := make(chan struct{})
	go s.WatchPodEvents(start_watch)
	start_watch <- struct{}{}
}
