package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	// "github.com/gobuffalo/packr"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"
)

var botWebhook string = os.Getenv("Chatbot")
var prometheusAddress string = os.Getenv("PrometheusAddress")
var notificationServer string = os.Getenv("NotificationServer")

func main() {
	client, err := api.NewClient(api.Config{
		Address: prometheusAddress,
	})
	if err != nil {
		fmt.Printf("Error creating client: %v\n", err)
		os.Exit(1)
	}

	// testNewResource(client)

	err = sendResource(client, notificationServer)
	if err != nil {
		fmt.Println(err)
	}
}

func sendResource(client api.Client, notificationServer string) error {
	var err error
	resource := make(map[string]map[string]string)
	keySort := make(map[string][]string)

	// Milvus Instance Total
	instanceCountMetric := "sum(milvus_total_count) by (cluster)"
	err = MilvusTotalNum(instanceCountMetric, client, resource, keySort, "Total")
	if err != nil {
		fmt.Println(err)
	}

	// Milvus Instance status num
	metricName := "sum(milvus_total_count) by (cluster, status)"
	err = milvusStatusNum(metricName, client, resource, keySort)
	if err != nil {
		fmt.Println(err)
	}

	// CPU Usage
	cpuUsageMetric := "1- sum(avg by (cluster, mode) (rate(node_cpu_seconds_total{job=\"node-exporter\", mode=~\"idle|steal\"}[1m]))) by (cluster)"
	err = getResourceDetail(cpuUsageMetric, client, resource, keySort, "cpuUsage")
	if err != nil {
		fmt.Println(err)
	}

	// CPU Request
	cpuRequestMetric := "sum(namespace_cpu:kube_pod_container_resource_requests:sum) by (cluster) / sum(kube_node_status_allocatable{resource=\"cpu\"}) by (cluster)"
	err = getResourceDetail(cpuRequestMetric, client, resource, keySort, "cpuRequest")
	if err != nil {
		fmt.Println(err)
	}

	// CPU Limit
	cpuLimitMetric := "sum(kube_pod_container_resource_limits{resource=\"cpu\"}) by (cluster) / sum(kube_node_status_allocatable{resource=\"cpu\"}) by (cluster)"
	err = getResourceDetail(cpuLimitMetric, client, resource, keySort, "cpuLimit")
	if err != nil {
		fmt.Println(err)
	}

	// Memory Usage
	memoryUsageMetric := "1 - sum(:node_memory_MemAvailable_bytes:sum) by(cluster) / sum(node_memory_MemTotal_bytes{job=\"node-exporter\"}) by(cluster)"
	err = getResourceDetail(memoryUsageMetric, client, resource, keySort, "memoryUsage")
	if err != nil {
		fmt.Println(err)
	}

	// Memory Request
	memoryRequestMetric := "sum(namespace_memory:kube_pod_container_resource_requests:sum{}) by (cluster) / sum(kube_node_status_allocatable{resource=\"memory\"}) by(cluster)"
	err = getResourceDetail(memoryRequestMetric, client, resource, keySort, "memoryRequest")
	if err != nil {
		fmt.Println(err)
	}

	// Memory Limit
	memoryLimitMetric := "sum(kube_pod_container_resource_limits{resource=\"memory\"}) by (cluster) / sum(kube_node_status_allocatable{resource=\"memory\"}) by (cluster)"
	err = getResourceDetail(memoryLimitMetric, client, resource, keySort, "memoryLimit")
	if err != nil {
		fmt.Println(err)
	}

	//resource group by node label
	err = getResourceGroupByNode(client, resource, keySort)
	if err != nil {
		fmt.Println(err)
	}

	//Send to feishu
	fmt.Println(resource)
	for clusterName, r := range resource {
		reader, err := requestBody(r, clusterName, keySort[clusterName])
		if err != nil {
			return err
		}
		fmt.Println("send resource details for cluster: ", clusterName)
		err = post(notificationServer, "application/json", reader)
		if err != nil {
			fmt.Println(err)
		}
	}
	return nil
}

func MilvusTotalNum(metricName string, client api.Client, resource map[string]map[string]string, keySort map[string][]string, field string) error {
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		return err
	}
	for _, result := range requestResult {
		cluster := string(result.Metric["cluster"])
		value := fmt.Sprintf("%.0f", result.Value)
		if _, ok := resource[cluster]; !ok {
			resource[cluster] = make(map[string]string)
			keySort[cluster] = make([]string, 0)
		}
		resource[cluster][field] = value
		keySort[cluster] = append(keySort[cluster], field)
	}
	return nil
}

func milvusStatusNum(metricName string, client api.Client, resource map[string]map[string]string, keySort map[string][]string) error {
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		return err
	}
	for _, result := range requestResult {
		cluster := string(result.Metric["cluster"])
		status := string(result.Metric["status"])
		value := fmt.Sprintf("%.0f", result.Value)

		if _, ok := resource[cluster]; ok {
			resource[cluster][status] = value
			keySort[cluster] = append(keySort[cluster], status)
			// resource[cluster] = make(map[string]string)
		}
	}
	return nil
}

func getResourceDetail(metricName string, client api.Client, resource map[string]map[string]string, keySort map[string][]string, field string) error {
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		return err
	}
	for _, result := range requestResult {
		cluster := string(result.Metric["cluster"])
		value := fmt.Sprintf("%.2f", result.Value*100) + "%"
		if _, ok := resource[cluster]; ok {
			resource[cluster][field] = value
			keySort[cluster] = append(keySort[cluster], field)
			// resource[cluster] = make(map[string]string)
		}
	}
	return nil
}


func getGroupNodeInstance(client api.Client, resource map[string]map[string]string, keySort map[string][]string, label string, field string) error {
	for clusterName, _ := range resource {
		// if _, ok := resource[clusterName]; ok {
		// metricName := "count(kube_node_labels{cluster="milvus-us-west-2-01", label_eks_amazonaws_com_nodegroup="infra-2c-8g-200g"})"
		metricName := fmt.Sprintf("count(kube_node_labels{cluster=\"%s\", label_eks_amazonaws_com_nodegroup=\"%s\"})", clusterName, label)
		requestResult, err := getQuery(client, metricName)
		if err != nil {
			fmt.Println(err)
			// return nil
		}
		if len(requestResult) > 0 {
			value := fmt.Sprintf("%.0f", requestResult[0].Value)
			resource[clusterName][field] = value
			keySort[clusterName] = append(keySort[clusterName], field)
		}
	}
	return nil
}


func getResourceGroupByNode(client api.Client, resource map[string]map[string]string, keySort map[string][]string) error {
	metricName := "sum(kube_node_labels) by (label_eks_amazonaws_com_nodegroup)"
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		return err
	}

	labels := []string{}
	for _, result := range requestResult {
		label := string(result.Metric["label_eks_amazonaws_com_nodegroup"])
		if label != "" {
			labels = append(labels, label)
		}
	}
	fmt.Println(labels)

	for _, label := range labels {
		// Group node instance
		_ = getGroupNodeInstance(client, resource, keySort, label, "NodeCount_"+label)
		// if err != nil {
		// 	fmt.Println(err)
		// }

		// group cpu usage
		groupMetricName := "sum(sum(1 - rate(node_cpu_seconds_total{mode=~\"idle\"}[1m]) * on(namespace, pod) group_left(node) node_namespace_pod:kube_pod_info:{}) by (cluster, node) * on(node) group_left(label_eks_amazonaws_com_nodegroup) kube_node_labels{label_eks_amazonaws_com_nodegroup=\"" + label + "\"}) by(cluster) /sum(kube_node_status_allocatable{resource=\"cpu\"} * on(node) group_left(label_eks_amazonaws_com_nodegroup) kube_node_labels {label_eks_amazonaws_com_nodegroup=\"" + label + "\"}) by (cluster)"
		err = getResourceDetail(groupMetricName, client, resource, keySort, "cpuUsage_"+label)
		if err != nil {
			fmt.Println(err)
		}

		// group cpu request
		groupMetricName = "sum(kube_pod_container_resource_requests{resource=\"cpu\"} * on (node) group_left(label_eks_amazonaws_com_nodegroup) kube_node_labels {label_eks_amazonaws_com_nodegroup=\"" + label + "\"}) by(cluster) / sum(kube_node_status_allocatable{resource=\"cpu\"} * on(node) group_left(label_eks_amazonaws_com_nodegroup) kube_node_labels {label_eks_amazonaws_com_nodegroup=\"" + label + "\"}) by (cluster)"
		err = getResourceDetail(groupMetricName, client, resource, keySort, "cpuRequest_"+label)
		if err != nil {
			fmt.Println(err)
		}

		// group cpu limit
		groupMetricName = "sum(kube_pod_container_resource_limits{resource=\"cpu\"} * on (node) group_left(label_eks_amazonaws_com_nodegroup) kube_node_labels {label_eks_amazonaws_com_nodegroup=\"" + label + "\"}) by(cluster) / sum(kube_node_status_allocatable{resource=\"cpu\"} * on(node) group_left(label_eks_amazonaws_com_nodegroup) kube_node_labels {label_eks_amazonaws_com_nodegroup=\"" + label + "\"}) by (cluster)"
		err = getResourceDetail(groupMetricName, client, resource, keySort, "cpuLimit_"+label)
		if err != nil {
			fmt.Println(err)
		}

		// group memory usage
		groupMetricName = "sum(sum((node_memory_MemTotal_bytes{job=\"node-exporter\"} - node_memory_MemAvailable_bytes{job=\"node-exporter\"}) * on (namespace, pod) group_left(node) node_namespace_pod:kube_pod_info:{}) by (cluster, node) * on(node) group_left(label_eks_amazonaws_com_nodegroup) kube_node_labels{label_eks_amazonaws_com_nodegroup=\"" + label + "\"}) by(cluster) /sum(kube_node_status_allocatable{resource=\"memory\"} * on(node) group_left(label_eks_amazonaws_com_nodegroup) kube_node_labels {label_eks_amazonaws_com_nodegroup=\"" + label + "\"}) by (cluster)"
		err = getResourceDetail(groupMetricName, client, resource, keySort, "memoryUsage_"+label)
		if err != nil {
			fmt.Println(err)
		}

		// group memory request
		groupMetricName = "sum(kube_pod_container_resource_requests{resource=\"memory\"} * on (node) group_left(label_eks_amazonaws_com_nodegroup) kube_node_labels {label_eks_amazonaws_com_nodegroup=\"" + label + "\"}) by(cluster) / sum(kube_node_status_allocatable{resource=\"memory\"} * on(node) group_left(label_eks_amazonaws_com_nodegroup) kube_node_labels {label_eks_amazonaws_com_nodegroup=\"" + label + "\"}) by (cluster)"
		err = getResourceDetail(groupMetricName, client, resource, keySort, "memoryRequest_"+label)
		if err != nil {
			fmt.Println(err)
		}

		// group memory limit
		groupMetricName = "sum(kube_pod_container_resource_limits{resource=\"memory\"} * on (node) group_left(label_eks_amazonaws_com_nodegroup) kube_node_labels {label_eks_amazonaws_com_nodegroup=\"" + label + "\"}) by(cluster) / sum(kube_node_status_allocatable{resource=\"memory\"} * on(node) group_left(label_eks_amazonaws_com_nodegroup) kube_node_labels {label_eks_amazonaws_com_nodegroup=\"" + label + "\"}) by (cluster)"
		err = getResourceDetail(groupMetricName, client, resource, keySort, "memoryLimit_"+label)
		if err != nil {
			fmt.Println(err)
		}
	}
	return nil
}

func getQuery(client api.Client, metricName string) ([]*model.Sample, error) {

	v1api := v1.NewAPI(client)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, warnings, err := v1api.Query(ctx, metricName, time.Now(), v1.WithTimeout(5*time.Second))
	if err != nil {
		// fmt.Printf("Error querying Prometheus: %v\n", err)
		return nil, err
	}

	if len(warnings) > 0 {
		fmt.Printf("Warnings: %v\n", warnings)
	}

	v, _ := result.(model.Vector)
	return v, nil
}

func requestBody(r map[string]string, cluster string, sortKey []string) (io.Reader, error) {
	requestBody, err := generateRequestBody()
	if err != nil {
		return nil, err
	}

	cstSh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, err
	}

	requestBody["alert"].(map[string]interface{})["alerts"].([]interface{})[0].(map[string]interface{})["status"] = cluster

	labels := requestBody["alert"].(map[string]interface{})["alerts"].([]interface{})[0].(map[string]interface{})["labels"].(map[string]interface{})
	labels["Time"] = fmt.Sprintf(time.Now().In(cstSh).Format("2006-01-02 15:04:05"))

	// for key, value := range r {
	// 	labels[key] = value
	// }

	message := "Cluster Resource Details \n Milvus Instance Count:"

	for i, key := range sortKey {
		if i < 11 {
			if i == 5 {
				message = fmt.Sprintf("%s\n\n Cluster Resource:", message)
			}
			message = fmt.Sprintf("%s\n  %s: %s", message, key, r[key])
		} else {
			keyName := strings.SplitN(key, "_", 2)
			if i == 11 {
				message = fmt.Sprintf("%s\n\n Node Group:", message)
			}
			if 0 == (i-11)%7 {
				message = fmt.Sprintf("%s\n    %s:", message, keyName[1])
			}
			message = fmt.Sprintf("%s\n      %s: %s", message, keyName[0], r[key])
		}
	}

	// fmt.Println(message)

	requestBody["alert"].(map[string]interface{})["alerts"].([]interface{})[0].(map[string]interface{})["annotations"].(map[string]interface{})["message"] = message + "\n"

	bytesData, _ := json.Marshal(requestBody)
	reader := bytes.NewReader(bytesData)
	return reader, nil
}

func generateRequestBody() (map[string]interface{}, error) {
	// box := packr.NewBox("alert")
	// byteValue := box.String("alert.json")
	jsonFile, err := os.Open("alert.json")
	if err != nil {
		return nil, err
	}
	defer jsonFile.Close()
	byteValue, _ := ioutil.ReadAll(jsonFile)

	var requestBody map[string]interface{}
	json.Unmarshal([]byte(byteValue), &requestBody)

	feishu := requestBody["receiver"].(map[string]interface{})["spec"].(map[string]interface{})["feishu"].(map[string]interface{})
	feishu["chatbot"].(map[string]interface{})["webhook"].(map[string]interface{})["value"] = botWebhook
	return requestBody, nil
}

func post(url string, contentType string, jsonFile io.Reader) error {
	client := http.Client{}
	rsp, err := client.Post(url, contentType, jsonFile)
	if err != nil {
		return err
	}
	defer rsp.Body.Close()

	body, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		return err
	}
	fmt.Println("RSP:", string(body))
	return nil
}

func testNewResource(client api.Client) {
	// Query
	// metricName := "sum(namespace_cpu:kube_pod_container_resource_requests:sum) by (cluster) / sum(kube_node_status_allocatable{resource=\"cpu\"}) by (cluster)"
	metricName := "count(kube_node_labels{cluster=\"uat-test\"}) by (label_eks_amazonaws_com_nodegroup)"
	// metricName := "count(kube_node_labels{cluster=\"uat-test\", label_eks_amazonaws_com_nodegroup=\"milvus-diskann-8c-32g\"})"
	// metricName := "sum(milvus_total_count) by (cluster)"
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(requestResult)

	// // Get Resource
	// resource := make(map[string]map[string]string)
	// keySort := make(map[string][]string)
	// err = getResourceDetail(metricName, client, resource, keySort, "CPUReauest")
	// if err != nil {
	// 	fmt.Println(err)
	// 	return
	// }
	// fmt.Println(resource)

	// // Send
	// for clusterName, r := range resource {
	// 	reader, err := requestBody(r, clusterName, keySort[clusterName])
	// 	if err != nil {
	// 		return
	// 	}
	// 	fmt.Println("send resource details for cluster: ", clusterName)
	// 	err = post(notificationServer, "application/json", reader)
	// 	if err != nil {
	// 		fmt.Println(err)
	// 	}
	// }
}
