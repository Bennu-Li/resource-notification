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

type milvusInstanceNum struct {
	ClusterName string
	Total       string
	Creating    string
	Deleting    string
	Healthy     string
	Unhealthy   string
	Time        string
}

type resourceDetail struct {
	ClusterName   string
	CPUReauest    string
	CPUUsage      string
	MemoryRequest string
	MemoryUsage   string
	Time          string
}

func main() {

	client, err := api.NewClient(api.Config{
		Address: prometheusAddress,
	})
	if err != nil {
		fmt.Printf("Error creating client: %v\n", err)
		os.Exit(1)
	}

	err = sendMilvusInstanceNum(client, notificationServer)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	err = sendResourceDetail(client, notificationServer)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}

func newMilvusInstanceNum(clusterName string, total string) *milvusInstanceNum {
	return &milvusInstanceNum{
		ClusterName: clusterName,
		Total:       total,
	}
}

func newResourceDetail(clusterName string) *resourceDetail {
	return &resourceDetail{
		ClusterName: clusterName,
	}
}

func sendMilvusInstanceNum(client api.Client, notificationServer string) error {
	milvusInstance := make(map[string]*milvusInstanceNum)
	timeStr := fmt.Sprintf(time.Now().Format("2006-01-02 15:04:05"))

	metricName := "sum(milvus_total_count) by (cluster)"
	totalResults, err := getQuery(client, metricName)
	if err != nil {
		return err
	}

	metricName = "sum(milvus_total_count) by (cluster, status)"
	statusResults, err := getQuery(client, metricName)
	if err != nil {
		return err
	}

	for _, result := range totalResults {
		cluster := string(result.Metric["cluster"])
		value := fmt.Sprintf("%.0f", result.Value)
		milvusInstance[cluster] = newMilvusInstanceNum(cluster, value)
	}

	for _, result := range statusResults {
		cluster := string(result.Metric["cluster"])
		status := string(result.Metric["status"])
		value := fmt.Sprintf("%.0f", result.Value)
		switch status {
		case "Creating":
			milvusInstance[cluster].Creating = value
		case "Deleting":
			milvusInstance[cluster].Deleting = value
		case "Healthy":
			milvusInstance[cluster].Healthy = value
		case "Unhealthy":
			milvusInstance[cluster].Unhealthy = value
		}
		milvusInstance[cluster].Time = timeStr
	}

	for _, result := range totalResults {
		cluster := string(result.Metric["cluster"])
		reader, err := milvusInstance[cluster].MilvusRequestBody()
		if err != nil {
			return err
		}
		fmt.Println("send milvus instance count notifications for cluster: ", cluster)
		err = post(notificationServer, "application/json", reader)
		if err != nil {
			fmt.Println(err)
		}
	}
	return nil
}

func (m *milvusInstanceNum) MilvusRequestBody() (io.Reader, error) {
	requestBody, err := generateRequestBody()
	if err != nil {
		return nil, err
	}
	title := "Milvus instance count"

	alert := requestBody["alert"].(map[string]interface{})["alerts"].([]interface{})[0].(map[string]interface{})
	alert["annotations"].(map[string]interface{})["message"] = title
	alert["labels"].(map[string]interface{})["Cluster"] = m.ClusterName
	alert["labels"].(map[string]interface{})["Total"] = m.Total
	alert["labels"].(map[string]interface{})["creating"] = m.Creating
	alert["labels"].(map[string]interface{})["deleting"] = m.Deleting
	alert["labels"].(map[string]interface{})["healthy"] = m.Healthy
	alert["labels"].(map[string]interface{})["unhealthy"] = m.Unhealthy
	alert["labels"].(map[string]interface{})["Time"] = m.Time

	bytesData, _ := json.Marshal(requestBody)
	reader := bytes.NewReader(bytesData)
	return reader, nil
}

func sendResourceDetail(client api.Client, notificationServer string) error {
	resource := make(map[string]*resourceDetail)
	timeStr := fmt.Sprintf(time.Now().Format("2006-01-02 15:04:05"))

	// CPU Request
	cpuRequestMetric := "sum(namespace_cpu:kube_pod_container_resource_requests:sum) by (cluster) / sum(kube_node_status_allocatable{resource=\"cpu\"}) by (cluster)"
	cpuRequestResult, err := getQuery(client, cpuRequestMetric)
	if err != nil {
		return err
	}
	for _, result := range cpuRequestResult {
		cluster := string(result.Metric["cluster"])
		value := fmt.Sprintf("%.2f", result.Value*100)
		if _, ok := resource[cluster]; !ok {
			resource[cluster] = newResourceDetail(cluster)
		}
		resource[cluster].CPUReauest = value + "%"
	}

	// CPU Usage
	cpuUsageMetric := "1- sum(avg by (cluster, mode) (rate(node_cpu_seconds_total{job=\"node-exporter\", mode=~\"idle|steal\"}[1m]))) by (cluster)"
	cpuUsageResult, err := getQuery(client, cpuUsageMetric)
	if err != nil {
		return err
	}
	for _, result := range cpuUsageResult {
		cluster := string(result.Metric["cluster"])
		value := fmt.Sprintf("%.2f", result.Value*100)
		if _, ok := resource[cluster]; !ok {
			resource[cluster] = newResourceDetail(cluster)
		}
		resource[cluster].CPUUsage = value + "%"
	}

	// Memory Request
	memoryRequestMetric := "sum(namespace_memory:kube_pod_container_resource_requests:sum{}) by (cluster) / sum(kube_node_status_allocatable{resource=\"memory\"}) by(cluster)"
	memoryRequestResult, err := getQuery(client, memoryRequestMetric)
	if err != nil {
		return err
	}
	for _, result := range memoryRequestResult {
		cluster := string(result.Metric["cluster"])
		value := fmt.Sprintf("%.2f", result.Value*100)
		if _, ok := resource[cluster]; !ok {
			resource[cluster] = newResourceDetail(cluster)
		}
		resource[cluster].MemoryRequest = value + "%"
		// fmt.Println(resource[cluster])
	}

	// Memory Usage
	memoryUsageMetric := "1 - sum(:node_memory_MemAvailable_bytes:sum) by(cluster) / sum(node_memory_MemTotal_bytes{job=\"node-exporter\"}) by(cluster)"
	memoryUsageResult, err := getQuery(client, memoryUsageMetric)
	if err != nil {
		return err
	}
	for _, result := range memoryUsageResult {
		cluster := string(result.Metric["cluster"])
		value := fmt.Sprintf("%.2f", result.Value*100)
		if _, ok := resource[cluster]; !ok {
			resource[cluster] = newResourceDetail(cluster)
		}
		resource[cluster].MemoryUsage = value + "%"
		resource[cluster].Time = timeStr
		// fmt.Println(resource[cluster])
		reader, err := resource[cluster].ResourceRequestBody()
		if err != nil {
			return err
		}
		fmt.Println("send resource details notifications for cluster: ", cluster)
		err = post(notificationServer, "application/json", reader)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *resourceDetail) ResourceRequestBody() (io.Reader, error) {
	requestBody, err := generateRequestBody()
	if err != nil {
		return nil, err
	}
	title := "Resource Request and Usage Detail"

	alert := requestBody["alert"].(map[string]interface{})["alerts"].([]interface{})[0].(map[string]interface{})
	alert["annotations"].(map[string]interface{})["message"] = title
	alert["labels"].(map[string]interface{})["Cluster"] = r.ClusterName
	alert["labels"].(map[string]interface{})["cpu request"] = r.CPUReauest
	alert["labels"].(map[string]interface{})["cpu usage"] = r.CPUUsage
	alert["labels"].(map[string]interface{})["memory request"] = r.MemoryRequest
	alert["labels"].(map[string]interface{})["memory usage"] = r.MemoryUsage
	alert["labels"].(map[string]interface{})["Time"] = r.Time

	bytesData, _ := json.Marshal(requestBody)
	reader := bytes.NewReader(bytesData)
	return reader, nil
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
