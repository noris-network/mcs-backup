package tasks

import (
	"bytes"
	_ "embed"
	"fmt"
	"int/util"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/bitfield/script"
	"github.com/goyek/goyek/v2"
)

const registryName = "local-registry"
const clusterName = "mcs-backup-int-test"

type KV map[string]string

var registry string
var debug = os.Getenv("MCS_BACKUP_DEBUG") == "true"

// TaskCreateCluster creates the k3s cluster
func TaskCreateCluster() goyek.Task {
	return goyek.Task{
		Name:  "create-cluster",
		Usage: "create k3s cluster in docker",
		Action: func(tf *goyek.A) {

			node0 := "k3d-" + clusterName + "-server-0"

			out, err := script.
				Exec("k3d cluster create --no-lb " + clusterName +
					" --registry-create " + registryName +
					` --k3s-arg "--disable=traefik@server:0"`).
				Stdout()
			if err != nil {
				tf.Errorf("k3d cluster create: %v", out)
			}

			hostname, err := script.
				Exec("kubectl cluster-info dump").
				JQ(`.items[0].metadata.labels."kubernetes.io/hostname"`).
				String()
			if err != nil {
				tf.Errorf("kubectl: %v", err)
			}

			if strings.Trim(strings.TrimSpace(hostname), `"`) != node0 {
				tf.Errorf("kubectl is not pointing to the local test cluster, found %q", hostname)
			}

			lines, err := script.Exec("docker ps").Match(node0).CountLines()
			if err != nil {
				tf.Errorf("docker ps: %v", err)
			}
			if lines != 1 {
				if err != nil {
					tf.Errorf("k3s container not found")
				}
			}
		},
	}
}

// TaskRunKustomize
func TaskRunKustomize(name, action, directory string) goyek.Task {
	return goyek.Task{
		Name:  name,
		Usage: action + " kustomization in " + directory,
		Action: func(tf *goyek.A) {
			out, err := script.
				//Exec("kustomize build " + directory).
				Exec("kubectl " + action + " -k " + directory).
				String()
			if err != nil {
				tf.Errorf("kustomize build: %v", out)
			}
		},
	}
}

// TaskDeleteCluster deletes cluster and registry
func TaskDeleteCluster() goyek.Task {
	return goyek.Task{
		Name:  "delete-cluster",
		Usage: "delete k3s cluster and registry in docker",
		Action: func(tf *goyek.A) {

			node0 := "k3d-" + clusterName + "-server-0"

			out, err := script.
				Exec("k3d cluster delete " + clusterName).
				String()
			if err != nil {
				tf.Errorf("k3d cluster delete: %v", out)
			}

			lines, err := script.Exec("docker ps").Match(node0).CountLines()
			if err != nil {
				tf.Errorf("docker ps: %v", err)
			}
			if lines > 0 {
				if err != nil {
					tf.Errorf("k3s container still running")
				}
			}
		},
	}
}

func TaskRunExternalCommand(name, externalCommand string) goyek.Task {
	return goyek.Task{
		Name:  name,
		Usage: fmt.Sprintf("run %v, if it exists", externalCommand),
		Action: func(tf *goyek.A) {

			out, err := script.
				IfExists(externalCommand).
				Exec(externalCommand).
				String()
			if err != nil && !strings.Contains(out, "no such file or directory") {
				tf.Errorf("script: %v", out)
			}
			if err != nil && strings.Contains(out, "no such file or directory") {
				tf.Logf("skip %v", externalCommand)
			}
		},
	}
}

// TaskGetRegistryAddress get the registry address from docker
func TaskGetRegistryAddress() goyek.Task {
	return goyek.Task{
		Name:  "get-registry-address",
		Usage: "get the registry address from docker",
		Action: func(tf *goyek.A) {

			listen, err := script.
				Exec(`docker ps -f name=` + registryName + ` --format "{{.Ports}}"`).String()
			if err != nil {
				tf.Errorf("docker ps: %v", err)
			}
			_, ports, _ := strings.Cut(listen, ":")
			port, _, _ := strings.Cut(ports, "->")
			if port == "" {
				tf.Errorf("port not found: %v", err)
			}
			registry = "localhost:" + port
			tf.Logf("internal registry is at %q", registry)
		},
	}
}

// TaskDeployYaml deploy (apply) the given yaml
func TaskDeployYaml(name, yaml string) goyek.Task {
	return goyek.Task{
		Name:  "deploy-" + name,
		Usage: "deploy " + name + " in test cluster",
		Action: func(tf *goyek.A) {
			if err := ApplyYaml(yaml); err != nil {
				tf.Error(err)
			}
		},
	}
}

// TaskDelete deletes the given object
func TaskDelete(namespace, kind, name string) goyek.Task {
	return goyek.Task{
		Name:  fmt.Sprintf("delete-%v/%v/%v", namespace, kind, name),
		Usage: fmt.Sprintf("delete %v %q in namespace %v", kind, name, namespace),
		Action: func(tf *goyek.A) {

			out, err := script.
				Exec(fmt.Sprintf(
					"kubectl delete -n %q --ignore-not-found=true --wait=true %v/%v",
					namespace, kind, name,
				)).
				String()
			if err != nil {
				tf.Errorf("kubctl delete: %v", out)
			}
		},
	}
}

// TaskDeployConfigMap
func TaskDeployConfigMap(namespace, name string, data KV) goyek.Task {
	return goyek.Task{
		Name:  "create-cm:" + name,
		Usage: "create the configmap " + name,
		Action: func(tf *goyek.A) {
			err := ApplyConfigMap(namespace, name, data)
			if err != nil {
				tf.Error(err)
			}
		},
	}
}

func TaskApplyYaml(taskName, namespace, yaml string) goyek.Task {
	return goyek.Task{
		Name:  "apply-yaml:" + taskName,
		Usage: "apply the given yaml",
		Action: func(tf *goyek.A) {
			if err := ApplyYaml(yaml); err != nil {
				tf.Error(err)
			}
		},
	}
}

func TaskApplyYamTmpl(taskName, namespace, yaml string, kv KV) goyek.Task {
	return goyek.Task{
		Name:  "apply-yaml-template:" + taskName,
		Usage: "apply the given yaml template",
		Action: func(tf *goyek.A) {
			if err := ApplyYamlTmpl(yaml, kv); err != nil {
				tf.Error(err)
			}
		},
	}
}

func TaskWaitPodReady(namespace, label string) goyek.Task {
	return goyek.Task{
		Name:  "wait-pod-ready:" + label,
		Usage: "wait until the pod is ready",
		Action: func(tf *goyek.A) {
			if err := WaitPodReady(namespace, label); err != nil {
				tf.Error(err)
			}
		},
	}
}

func ApplyConfigMap(namespace, name string, data KV) error {
	cm := fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %q\n  namespace: %q\ndata:\n", name, namespace)
	for k, v := range data {
		cm += fmt.Sprintf("  %v: %q\n", k, v)
	}
	return ApplyYaml(cm)
}

func ApplyYaml(yaml string) error {
	return ApplyYamlTmpl(yaml, map[string]string{})
}

func ApplyYamlTmpl(yaml string, kv KV) error {
	tmpl, err := template.New("").Parse(yaml)
	if err != nil {
		return fmt.Errorf("parse: %v", err)
	}
	bb := bytes.Buffer{}
	err = tmpl.Execute(&bb, kv)
	if err != nil {
		return fmt.Errorf("execute: %v", err)
	}
	if debug {
		util.PrintDebug("YAML", bb.String())
	}
	out, err := script.Echo(bb.String()).Exec("kubectl apply -f -").String()
	if err != nil {
		return fmt.Errorf("kubectl apply: %v", out)
	}
	return nil
}

func InstallPrometheusOperator() goyek.Task {
	return goyek.Task{
		Name:  "install-prom-op",
		Usage: "install the prometheus operator",
		Action: func(tf *goyek.A) {
			out, err := script.Exec("kubectl create -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/bundle.yaml").String()
			if err != nil {
				tf.Errorf("kubectl create prom: %v", out)
				return
			}
			out, err = script.Exec(`kubectl patch prometheus prometheus -n monitoring --type=merge --patch '{"spec":{"serviceMonitorNamespaceSelector":{"matchLabels":{"prometheus":"enabled"}}}}'`).String()
			if err != nil {
				tf.Errorf("kubectl create crb: %v", out)
				return
			}
		},
	}
}

func WaitPodReady(namespace, label string) error {
	remain := 12
	for {
		remain--
		out, err := script.
			Exec(fmt.Sprintf(
				"kubectl wait --for=condition=ready --timeout=90s pod -l %v -n %v",
				label, namespace),
			).String()
		if err != nil {
			if remain > 0 && strings.Contains(out, "no matching resources found") {
				time.Sleep(5 * time.Second)
				continue
			}
			return fmt.Errorf("kubectl wait: %v", out)
		}
		break
	}
	return nil
}

func TaskWaitAllPodsReady() goyek.Task {
	return goyek.Task{
		Name:  "wait-all-pods-ready",
		Usage: "wait until all pods are ready",
		Action: func(tf *goyek.A) {
			out, err := script.
				Exec(`kubectl get pods --all-namespaces --output=go-template='{{ range .items}}-n {{.metadata.namespace}} pod/{{.metadata.name}}{{"\n"}}{{end}}'`).
				Reject("helper-pod-").
				ExecForEach("kubectl wait --for=condition=ready --timeout=90s {{.}}").
				String()
			if err != nil {
				tf.Errorf("kubectl wait: %v", out)
			} else {
				tf.Log(out)
			}
		},
	}
}
