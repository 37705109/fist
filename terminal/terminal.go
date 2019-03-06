package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/clientcmd"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

//consts
const (
	DefaultTTYnameapace = "sealyun-tty"
	DefaultApiserver    = "https://kubernetes.default.svc.cluster.local:443" //or https://10.96.0.1:443
	DefaultKubeTTYimage = "fanux/fist-tty-tools:v1.0.0"
)

//Terminal is
type Terminal struct {
	//input field
	User           string
	UserToken      string
	Apiserver      string // just using default apiserver
	Namespace      string // the kubeconfig default context namespace
	WithoutToken   bool   // if true, mount the kubeconfig file, using ttyd instead the start-terminal.sh
	KubeConfigPath string //default is  "/root/.kube/config"
	TTYKubeImage   string //default is  "fanux/fist-tty-tools:v1.0.0"

	//output append field
	TerminalID string
	EndPoint   string
}

func newTerminal() *Terminal {
	return &Terminal{
		Namespace:    "default",
		WithoutToken: false,
		Apiserver:    DefaultApiserver,
		TTYKubeImage: DefaultKubeTTYimage,
	}
}

func newUUID() string {
	u := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, u); err != nil {
		panic(err)
	}

	u[8] = (u[8] | 0x80) & 0xBF
	u[6] = (u[6] | 0x40) & 0x4F

	return hex.EncodeToString(u)
}

//Create a terminal
func (t *Terminal) Create() error {
	t.TerminalID = newUUID()

	//create tty deployment and service
	return CreateTTYcontainer(t)
}

//CreateTTYnamespace
func CreateTTYnamespace(clientset *kubernetes.Clientset) error {
	_, err := clientset.CoreV1().Namespaces().Get(DefaultTTYnameapace, metav1.GetOptions{})
	if err != nil {
		_, err = clientset.CoreV1().Namespaces().Create(&apiv1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: DefaultTTYnameapace,
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

//CreateTTYdeploy
func CreateTTYdeploy(t *Terminal, clientset *kubernetes.Clientset, re int32) error {
	//get deploy deployClient
	deployClient := clientset.AppsV1().Deployments(DefaultTTYnameapace)
	//vars
	var (
		objMeta         metav1.ObjectMeta
		selector        *metav1.LabelSelector
		templateObjMeta metav1.ObjectMeta
		ports           []apiv1.ContainerPort
		env             []apiv1.EnvVar
		volume          []apiv1.Volume
		volumeMounts    []apiv1.VolumeMount
	)
	//init
	objMeta = metav1.ObjectMeta{
		Name: "deploy-" + t.TerminalID,
	}
	selector = &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"TerminalID": t.TerminalID,
		},
	}
	templateObjMeta = metav1.ObjectMeta{
		Labels: map[string]string{
			"TerminalID": t.TerminalID,
		},
	}
	ports = []apiv1.ContainerPort{
		{
			Name:          "http",
			Protocol:      apiv1.ProtocolTCP,
			ContainerPort: 8080,
		},
	}
	if t.WithoutToken {
		env = []apiv1.EnvVar{
			{Name: "TERMINAL_ID", Value: t.TerminalID},
		}
		secretsClient := clientset.CoreV1().Secrets(DefaultTTYnameapace)
		secretsData, _ := ioutil.ReadFile(t.KubeConfigPath)
		secretsClient.Create(&apiv1.Secret{
			Type: apiv1.SecretTypeOpaque,
			ObjectMeta: metav1.ObjectMeta{
				Name: "secret-" + t.TerminalID,
			},
			Data: map[string][]byte{
				"config": secretsData,
			},
		})
		volume = []apiv1.Volume{
			{
				Name: "kube-config",
				VolumeSource: apiv1.VolumeSource{
					Secret: &apiv1.SecretVolumeSource{
						SecretName: "secret-" + t.TerminalID,
					},
				},
			},
		}
		volumeMounts = []apiv1.VolumeMount{
			{
				Name:      "kube-config",
				MountPath: "/root/.kube",
				ReadOnly:  true,
			},
		}
	} else {
		env = []apiv1.EnvVar{
			{Name: "APISERVER", Value: t.Apiserver},
			{Name: "USER_TOKEN", Value: t.UserToken},
			{Name: "NAMESPACE", Value: t.Namespace},
			{Name: "USER_NAME", Value: t.User},
			{Name: "TERMINAL_ID", Value: t.TerminalID},
		}
		volume = []apiv1.Volume{}
		volumeMounts = []apiv1.VolumeMount{}
	}
	_, err := deployClient.Create(&appsv1.Deployment{
		ObjectMeta: objMeta,
		Spec: appsv1.DeploymentSpec{
			Replicas: &re,
			Selector: selector,
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: templateObjMeta,
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Env:          env,
							Name:         "tty",
							Image:        t.TTYKubeImage,
							Ports:        ports,
							VolumeMounts: volumeMounts,
						},
					},
					Volumes: volume,
				},
			},
		},
	})
	if err != nil {
		return err
	}
	return nil
}

//CreateTTYservice
func CreateTTYservice(t *Terminal, clientset *kubernetes.Clientset) error {
	service, err := clientset.CoreV1().Services(DefaultTTYnameapace).Create(&apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "svc-" + t.TerminalID,
		},
		Spec: apiv1.ServiceSpec{
			Selector: map[string]string{
				"TerminalID": t.TerminalID,
			},
			Type: "NodePort",
			Ports: []apiv1.ServicePort{
				{Name: "tty", Port: 8080, TargetPort: intstr.FromInt(8080), Protocol: apiv1.Protocol("TCP")},
			},
		},
	})
	if err != nil {
		return err
	}
	t.EndPoint = fmt.Sprintf("%d", service.Spec.Ports[0].NodePort)
	return nil
}
func GetK8sClient(t *Terminal) (*kubernetes.Clientset, error) {
	var (
		config *rest.Config
		err    error
	)
	if t.WithoutToken {
		config, err = clientcmd.BuildConfigFromFlags("", t.KubeConfigPath)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientset, nil
}

//CreateTTYcontainer is
func CreateTTYcontainer(t *Terminal) error {
	//get client of k8s
	clientset, _ := GetK8sClient(t)
	var re int32
	// deploy  Replicas number
	re = 1
	//create namespace
	CreateTTYnamespace(clientset)
	//create deploy
	CreateTTYdeploy(t, clientset, re)
	//create service
	CreateTTYservice(t, clientset)
	return nil
}

//LoadTerminalID is
func LoadTerminalID() error {
	//TODO
	return nil
}
