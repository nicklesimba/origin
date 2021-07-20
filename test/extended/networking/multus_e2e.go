package networking

import (
	"context"
	"fmt"
	"strings"

	v1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	g "github.com/onsi/ginkgo"
	o "github.com/onsi/gomega"
	exutil "github.com/openshift/origin/test/extended/util"
	"github.com/openshift/origin/test/extended/util/image"
	kapiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	e2e "k8s.io/kubernetes/test/e2e/framework"
	frameworkpod "k8s.io/kubernetes/test/e2e/framework/pod"
)

var _ = g.Describe("[sig-network][Feature:Multus-CNI functionality]", func() {
	var oc *exutil.CLI
	var ns string // in this case just one pod needing one namespace

	// vvvvvv MIGHT BE GOOD TO DUMP LOGS ...... vvvvvv
	// vvvvvv NOT SURE IF I NEED THIS BLOCK ??? vvvvvv
	// this hook must be registered before the framework namespace teardown
	// hook
	// AfterEach(func() {
	// 	if CurrentGinkgoTestDescription().Failed {
	// 		// If test fails dump test pods logs
	// 		exutil.DumpPodLogsStartingWithInNamespace("acl-logging", ns[0], oc.AsAdmin())
	// 		exutil.DumpPodLogsStartingWithInNamespace("acl-logging", ns[1], oc.AsAdmin())
	// 		// Dump what audit logs looked like if test failed
	// 		e2e.Logf("Audit logs are incorrect:\n %v", auditOut)
	// 	}
	// })

	oc = exutil.NewCLI("multus_e2e")

	InOpenShiftSDNContext(
		func() {
			f := oc.KubeFramework()
			g.It("should ensure that multus-cni functions correctly", func() {
				makeNamespaceScheduleToAllNodes(f)
				ns = f.Namespace.Name
				fmt.Println()
				o.Expect(testMultus(f, oc, ns)).To(o.Succeed())
			})
		},
	)
})

// This test attempts to recreate the steps in the multus quickstart guide to ensure functionality.
// (See: https://github.com/k8snetworkplumbingwg/multus-cni/blob/master/docs/quickstart.md#storing-a-configuration-as-a-custom-resource)
// Creates a net-attach-def, creates a pod with an annotation that refers to it, and checks for net1 interface.
func testMultus(f *e2e.Framework, oc *exutil.CLI, ns string) error {
	// Setup
	var nodes [2]*kapiv1.Node // not sure if i need multiple nodes? just copying this for
	var err error
	podName := "multus-test-pod"

	nodes[0], nodes[1], err = findAppropriateNodes(f, DIFFERENT_NODE)
	expectNoError(err)

	g.By("attempting to use multus to create a pod with multiple network interfaces")

	// 1. First create net-attach-def using bridgeCNI
	nad := &v1.NetworkAttachmentDefinition{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{},
		Spec: v1.NetworkAttachmentDefinitionSpec{
			Config: "{\"name\": \"multustestbridge\",\"type\": \"bridge\",\"bridge\": \"multustestbr0\",\"isDefaultGateway\": true,\"forceAddress\": false,\"ipMasq\": true,\"hairpinMode\": true,\"ipam\": {\"type\": \"static\",\"addresses\": [{\"address\": \"10.10.0.1/24\"}]}",
		},
	}

	nadc, err := NewNetAttachDefClient("/tmp/kubeconfig") // need to put the proper path here. I think for me it's just /tmp/kubeconfig
	expectNoError(err)

	nadc.create(nad)

	// 2. Then need to make a pod with...
	// - an annotation: k8s.v1.cni.cncf.io/networks: multus-test-bridge-conf
	// - command: sleep infinitely (see simple-macvlan.yaml)
	testPod := launchTestMultusPod(f, nodes[0].Name, podName) // not sure about the nodes!

	// 3. Check that the pod is up
	// this is how andrew did it...
	_, err = waitForTestMultusPod(f, ns, podName) // not sure if i need "ip" return val
	expectNoError(err)

	// 4. Then need to inspect the pod and verify net1 interface exists.
	o.Expect(validMultusConfig(testPod, ns)).Should(o.Equal(true))
	return nil
}

// this function and other helpers may need to live in test/extended/util later.
// not sure if arguments are all needed. will clean up later
func launchTestMultusPod(f *e2e.Framework, nodeName string, podName string) *kapiv1.Pod {
	contName := fmt.Sprintf("%s-container", podName)
	pod := &kapiv1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind: "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: podName,
		},
		Spec: kapiv1.PodSpec{
			Containers: []kapiv1.Container{
				{
					Name:    contName,
					Image:   image.LocationFor("docker.io/openshift/test-multicast:latest"),
					Command: []string{"/bin/ash", "-c", "trap : TERM INT; sleep infinity & wait"},
				},
			},
			NodeName:      nodeName,
			RestartPolicy: kapiv1.RestartPolicyNever,
		},
	}
	// ^^^^ above pod image might need to be something like...this...
	// Image:           imageutils.GetE2EImage(imageutils.Agnhost)
	// not really sure yet.

	pod.ObjectMeta.Annotations = map[string]string{
		"k8s.v1.cni.cncf.io/networks": "multus-test-bridge-conf",
	}

	_, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).Create(context.Background(), pod, metav1.CreateOptions{})
	expectNoError(err)
	return pod
}

func waitForTestMultusPod(f *e2e.Framework, namespace string, podName string) (string, error) {
	var podIP string
	err := frameworkpod.WaitForPodCondition(f.ClientSet, f.Namespace.Name, podName, "running", podStartTimeout, func(pod *kapiv1.Pod) (bool, error) {
		podIP = pod.Status.PodIP
		return (podIP != "" && pod.Status.Phase != kapiv1.PodPending), nil
	})
	return podIP, err
}

// Need to look for existing device net1. How to query that? Probably with k8s api
func validMultusConfig(testPod *kapiv1.Pod, ns string) bool {
	containerName := testPod.Spec.Containers[0].Name
	podName := testPod.ObjectMeta.Name
	command := "ip a show dev net1"

	// For now I am assuming stdin for the command to be nil
	output, stderr, err := ExecToPodThroughAPI(command, containerName, podName, ns, nil)

	if len(stderr) != 0 {
		fmt.Println("STDERR:", stderr)
	}
	if err != nil {
		fmt.Printf("Error occured while `exec`ing to the Pod %q, namespace %q, command %q. Error: %+v\n", podName, ns, command, err)
	} else { // Need to parse output.
		fmt.Println("Output:") // printing it to see at least what's going on if it fails
		fmt.Println(output)

		if strings.Contains(output, "net1") {
			return true
		}
	}

	// failed to find device
	return false
}
