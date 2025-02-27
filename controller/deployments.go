package controller

import (
	"bytes"
	"context"
	"sort"
	"strings"

	"github.com/pkg/errors"
	"k8s.io/klog"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func rollingUpgradeDeployments(ctx context.Context, cm *v1.ConfigMap, c kubernetes.Interface) error {
	ns := cm.Namespace
	configMapName := cm.Name
	configMapVersion := convertConfigMapToToken(cm)

	deployments, err := c.ExtensionsV1beta1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to list deployments")
	}
	for _, d := range deployments.Items {
		containers := d.Spec.Template.Spec.Containers
		// match deployments with the correct annotation
		annotationValue, _ := d.ObjectMeta.Annotations[updateOnChangeAnnotation]
		if annotationValue != "" {
			values := strings.Split(annotationValue, ",")
			matches := false
			for _, value := range values {
				if value == configMapName {
					matches = true
					break
				}
			}
			if matches {
				updateContainers(containers, annotationValue, configMapVersion)

				// update the deployment
				_, err := c.ExtensionsV1beta1().Deployments(ns).Update(ctx, &d, metav1.UpdateOptions{})
				if err != nil {
					return errors.Wrap(err, "update deployment failed")
				}
				klog.Infof("Updated Deployment %s", d.Name)
			}
		}
	}
	return nil
}

// lets convert the configmap into a unique token based on the data values
func convertConfigMapToToken(cm *v1.ConfigMap) string {
	values := []string{}
	for k, v := range cm.Data {
		values = append(values, k+"="+v)
	}
	sort.Strings(values)
	text := strings.Join(values, ";")
	// we could zip and base64 encode,
	// but for now we could leave this easy to read so that it's easier to diagnose when & why things changed
	return text
}

func updateContainers(containers []v1.Container, annotationValue, configMapVersion string) bool {
	// we can have multiple configmaps to update
	answer := false
	configmaps := strings.Split(annotationValue, ",")
	for _, cmNameToUpdate := range configmaps {
		configmapEnvar := "FABRIC8_" + convertToEnvVarName(cmNameToUpdate) + "_CONFIGMAP"

		for i := range containers {
			envs := containers[i].Env
			matched := false
			for j := range envs {
				if envs[j].Name == configmapEnvar {
					matched = true
					if envs[j].Value != configMapVersion {
						klog.Infof("Updating %s to %s", configmapEnvar, configMapVersion)
						envs[j].Value = configMapVersion
						answer = true
					}
				}
			}
			// if no existing env var exists lets create one
			if !matched {
				e := v1.EnvVar{
					Name:  configmapEnvar,
					Value: configMapVersion,
				}
				containers[i].Env = append(containers[i].Env, e)
				answer = true
			}
		}
	}
	return answer
}

// convertToEnvVarName converts the given text into a usable env var
// removing any special chars with '_'
func convertToEnvVarName(text string) string {
	var buffer bytes.Buffer
	lower := strings.ToUpper(text)
	lastCharValid := false
	for i := 0; i < len(lower); i++ {
		ch := lower[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			buffer.WriteString(string(ch))
			lastCharValid = true
		} else {
			if lastCharValid {
				buffer.WriteString("_")
			}
			lastCharValid = false
		}
	}
	return buffer.String()
}
