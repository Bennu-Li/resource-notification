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

	err = sendResource(client, notificationServer)
	if err != nil {
		fmt.Println(err)
	}
}

func sendResource(client api.Client, notificationServer string) error {
	var err error
	resource := make(map[string]map[string]string)

	// CPU Request
	cpuRequestMetric := "sum(namespace_cpu:kube_pod_container_resource_requests:sum) by (cluster) / sum(kube_node_status_allocatable{resource=\"cpu\"}) by (cluster)"
	err = getResourceDetail(cpuRequestMetric, client, resource, "cpuRequest")
	if err != nil {
		fmt.Println(err)
	}

	// CPU Usage
	cpuUsageMetric := "1- sum(avg by (cluster, mode) (rate(node_cpu_seconds_total{job=\"node-exporter\", mode=~\"idle|steal\"}[1m]))) by (cluster)"
	err = getResourceDetail(cpuUsageMetric, client, resource, "cpuUsage")
	if err != nil {
		fmt.Println(err)
	}

	// Memory Request
	memoryRequestMetric := "sum(namespace_memory:kube_pod_container_resource_requests:sum{}) by (cluster) / sum(kube_node_status_allocatable{resource=\"memory\"}) by(cluster)"
	err = getResourceDetail(memoryRequestMetric, client, resource, "memoryRequest")
	if err != nil {
		fmt.Println(err)
	}

	// Memory Usage
	memoryUsageMetric := "1 - sum(:node_memory_MemAvailable_bytes:sum) by(cluster) / sum(node_memory_MemTotal_bytes{job=\"node-exporter\"}) by(cluster)"
	err = getResourceDetail(memoryUsageMetric, client, resource, "memoryUsage")
	if err != nil {
		fmt.Println(err)
	}

	// Milvus Instance Total
	instanceCountMetric := "sum(milvus_total_count) by (cluster)"
	err = MilvusTotalNum(instanceCountMetric, client, resource, "CountMilvusInstance")
	if err != nil {
		fmt.Println(err)
	}

	// Milvus Instance status num
	metricName := "sum(milvus_total_count) by (cluster, status)"
	err = milvusStatusNum(metricName, client, resource)
	if err != nil {
		fmt.Println(err)
	}

	//resource usage group by node label
	metricName = "kube_node_labels"
	err = getResourceGroupByNode(metricName, client, resource)
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println(resource)

	//Send to feishu
	for clusterName, r := range resource {
		reader, err := requestBody(r, clusterName)
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

func getResourceDetail(metricName string, client api.Client, resource map[string]map[string]string, field string) error {
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		return err
	}
	for _, result := range requestResult {
		cluster := string(result.Metric["cluster"])
		value := fmt.Sprintf("%.2f", result.Value*100) + "%"
		if _, ok := resource[cluster]; !ok {
			resource[cluster] = make(map[string]string)
		}
		resource[cluster][field] = value
	}
	return nil
}

func MilvusTotalNum(metricName string, client api.Client, resource map[string]map[string]string, field string) error {
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		return err
	}
	for _, result := range requestResult {
		cluster := string(result.Metric["cluster"])
		value := fmt.Sprintf("%.0f", result.Value)
		if _, ok := resource[cluster]; !ok {
			resource[cluster] = make(map[string]string)
		}
		resource[cluster][field] = value
	}
	return nil
}

func milvusStatusNum(metricName string, client api.Client, resource map[string]map[string]string) error {
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		return err
	}
	for _, result := range requestResult {
		cluster := string(result.Metric["cluster"])
		status := string(result.Metric["status"])
		value := fmt.Sprintf("%.0f", result.Value)

		if _, ok := resource[cluster]; !ok {
			resource[cluster] = make(map[string]string)
		}
		resource[cluster][status] = value
	}
	return nil
}

func getResourceGroupByNode(metricName string, client api.Client, resource map[string]map[string]string) error {
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		return err
	}

	labels := []string{}
	for _, result := range requestResult {
		for key, _ := range result.Metric {
			if len(key) > 22 && key[0:22] == "label_node_role_milvus" {
				labels = append(labels, string(key))
			}
		}
	}
	fmt.Println(labels)

	for _, label := range labels {
		// group cpu usage
		groupMetricName := "sum(kube_pod_container_resource_requests{resource=\"cpu\"} * on (node) group_left(" + label + ") kube_node_labels {" + label + "=\"true\"}) by(cluster) / sum(kube_node_status_allocatable{resource=\"cpu\"} * on(node) group_left(" + label + ") kube_node_labels {" + label + "=\"true\"}) by (cluster)"
		err = getResourceDetail(groupMetricName, client, resource, "cpuUsage_"+label[23:])
		if err != nil {
			fmt.Println(err)
		}

		// group memory usage
		groupMetricName = "sum(kube_pod_container_resource_requests{resource=\"memory\"} * on (node) group_left(" + label + ") kube_node_labels {" + label + "=\"true\"}) by(cluster) / sum(kube_node_status_allocatable{resource=\"memory\"} * on(node) group_left(" + label + ") kube_node_labels {" + label + "=\"true\"}) by (cluster)"
		err = getResourceDetail(groupMetricName, client, resource, "memoryUsage_"+label[23:])
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

func requestBody(r map[string]string, cluster string) (io.Reader, error) {
	requestBody, err := generateRequestBody()
	if err != nil {
		return nil, err
	}
	requestBody["alert"].(map[string]interface{})["alerts"].([]interface{})[0].(map[string]interface{})["status"] = cluster

	labels := requestBody["alert"].(map[string]interface{})["alerts"].([]interface{})[0].(map[string]interface{})["labels"].(map[string]interface{})

	for key, value := range r {
		labels[key] = value
	}
	cstSh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, err
	}

	labels["time"] = fmt.Sprintf(time.Now().In(cstSh).Format("2006-01-02 15:04:05"))
	// alert["annotations"].(map[string]interface{})["message"] = "Cluster Resource Usage Details"

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
	metricName := "sum(namespace_cpu:kube_pod_container_resource_requests:sum) by (cluster) / sum(kube_node_status_allocatable{resource=\"cpu\"}) by (cluster)"
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(requestResult)

	// Get Resource
	resource := make(map[string]map[string]string)
	err = getResourceDetail(metricName, client, resource, "CPUReauest")
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(resource)

	// Send
	for clusterName, r := range resource {
		reader, err := requestBody(r, clusterName)
		if err != nil {
			return
		}
		fmt.Println("send resource details for cluster: ", clusterName)
		err = post(notificationServer, "application/json", reader)
		if err != nil {
			fmt.Println(err)
		}
	}
}
