package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
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

type resourceUsageDetails struct {
	ClusterName        string
	CPUReauest         string
	CPUReauest8cu      string
	CPUUsage           string
	MemoryRequest      string
	MemoryRequest8cu   string
	MemoryUsage        string
	InstanceTotal      string
	Creating           string
	Deleting           string
	Healthy            string
	Unhealthy          string
}

func main() {
	client, err := api.NewClient(api.Config{
		Address: prometheusAddress,
	})
	if err != nil {
		fmt.Printf("Error creating client: %v\n", err)
		os.Exit(1)
	}

	
	// metricName := ""
	// requestResult, _ := getQuery(client, memoryRequest8cumetric)
	// fmt.Println(requestResult)

	// resource := make(map[string]*resourceUsageDetails)
	// getResource(CPURequest8cumetric, client, resource, "CPUReauest8cu")
	// fmt.Println(resource["uat-test"])

	err = sendResourceDetails(client, notificationServer)
	if err != nil {
		fmt.Println(err)
	}
}

func sendResourceDetails(client api.Client, notificationServer string) error {
	var err error
	resource := make(map[string]*resourceUsageDetails)

	// CPU Request
	cpuRequestMetric := "sum(namespace_cpu:kube_pod_container_resource_requests:sum) by (cluster) / sum(kube_node_status_allocatable{resource=\"cpu\"}) by (cluster)"
	err = getResource(cpuRequestMetric, client, resource, "CPUReauest")
	if err != nil {
		fmt.Println(err)
	}


	// CPU Usage
	cpuUsageMetric := "1- sum(avg by (cluster, mode) (rate(node_cpu_seconds_total{job=\"node-exporter\", mode=~\"idle|steal\"}[1m]))) by (cluster)"
	err = getResource(cpuUsageMetric, client, resource, "CPUUsage")
	if err != nil {
		fmt.Println(err)
	}

	// CPU Request for 8cu
	cpuRequest8cumetric := "sum(kube_pod_container_resource_requests{resource=\"cpu\"} * on (node) group_left(label_node_role_milvus_8cu_standalone) kube_node_labels {label_node_role_milvus_8cu_standalone=\"true\"}) by(cluster) / sum(kube_node_status_allocatable{resource=\"cpu\"} * on(node) group_left(label_node_role_milvus_8cu_standalone) kube_node_labels {label_node_role_milvus_8cu_standalone=\"true\"}) by (cluster)"
	err = getResource(cpuRequest8cumetric, client, resource, "CPUReauest8cu")
	if err != nil {
		fmt.Println(err)
	}

	// Memory Request for 8cu
	memoryRequest8cumetric := "sum(kube_pod_container_resource_requests{resource=\"memory\"} * on (node) group_left(label_node_role_milvus_8cu_standalone) kube_node_labels {label_node_role_milvus_8cu_standalone=\"true\"}) by(cluster) / sum(kube_node_status_allocatable{resource=\"memory\"} * on(node) group_left(label_node_role_milvus_8cu_standalone) kube_node_labels {label_node_role_milvus_8cu_standalone=\"true\"}) by (cluster)"
	err = getResource(memoryRequest8cumetric, client, resource, "MemoryRequest8cu")
	if err != nil {
		fmt.Println(err)
	}

	// Memory Request
	memoryRequestMetric := "sum(namespace_memory:kube_pod_container_resource_requests:sum{}) by (cluster) / sum(kube_node_status_allocatable{resource=\"memory\"}) by(cluster)"
	err = getResource(memoryRequestMetric, client, resource, "MemoryRequest")
	if err != nil {
		fmt.Println(err)
	}

	// Memory Usage
	memoryUsageMetric := "1 - sum(:node_memory_MemAvailable_bytes:sum) by(cluster) / sum(node_memory_MemTotal_bytes{job=\"node-exporter\"}) by(cluster)"
	err = getResource(memoryUsageMetric, client, resource, "MemoryUsage")
	if err != nil {
		fmt.Println(err)
	}

	// Milvus Instance Total
	instanceCountMetric := "sum(milvus_total_count) by (cluster)"
	err = getMilvustotal(instanceCountMetric, client, resource, "InstanceTotal")
	if err != nil {
		fmt.Println(err)
	}

	// Milvus Instance status num
	metricName := "sum(milvus_total_count) by (cluster, status)"
	err = getMilvusStatusNum(metricName, client, resource)
	if err != nil {
		fmt.Println(err)
	}

	for clusterName, r := range resource {
		reader, err := r.getRequestBody()
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

func newResourceUsageDetails(clusterName string) *resourceUsageDetails {
	return &resourceUsageDetails{
		ClusterName: clusterName,
	}
}

func getResource(metricName string, client api.Client, resource map[string]*resourceUsageDetails, field string) error {
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		return err
	}
	for _, result := range requestResult {
		cluster := string(result.Metric["cluster"])
		value := fmt.Sprintf("%.2f", result.Value*100) + "%"
		if _, ok := resource[cluster]; !ok {
			resource[cluster] = newResourceUsageDetails(cluster)
		}
		v := reflect.ValueOf(resource[cluster]).Elem()
		v.FieldByName(field).Set(reflect.ValueOf(value))
	}
	return nil
}

func getMilvustotal(metricName string, client api.Client, resource map[string]*resourceUsageDetails, field string) error {
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		return err
	}
	for _, result := range requestResult {
		cluster := string(result.Metric["cluster"])
		value := fmt.Sprintf("%.0f", result.Value)
		if _, ok := resource[cluster]; !ok {
			resource[cluster] = newResourceUsageDetails(cluster)
		}
		v := reflect.ValueOf(resource[cluster]).Elem()
		v.FieldByName(field).Set(reflect.ValueOf(value))
	}
	return nil
}

func getMilvusStatusNum(metricName string, client api.Client, resource map[string]*resourceUsageDetails) error {
	requestResult, err := getQuery(client, metricName)
	if err != nil {
		return err
	}
	for _, result := range requestResult {
		cluster := string(result.Metric["cluster"])
		status := string(result.Metric["status"])
		value := fmt.Sprintf("%.0f", result.Value)
		
		if _, ok := resource[cluster]; !ok {
			resource[cluster] = newResourceUsageDetails(cluster)
		}
		v := reflect.ValueOf(resource[cluster]).Elem()
		t := reflect.TypeOf(resource[cluster]).Elem()
		if _, ok := t.FieldByName(status); ok {
			v.FieldByName(status).Set(reflect.ValueOf(value))
		} else {
			fmt.Println("New Milvus status need to be recoreded")
		}
	}
	return nil
}

func (r *resourceUsageDetails) getRequestBody() (io.Reader, error) {
	requestBody, err := generateRequestBody()
	if err != nil {
		return nil, err
	}
	labels := requestBody["alert"].(map[string]interface{})["alerts"].([]interface{})[0].(map[string]interface{})["labels"].(map[string]interface{})
	// labels["Cluster"] = r.ClusterName
	labels["CPU request"] = r.CPUReauest
	labels["CPU usage"] = r.CPUUsage
	labels["Memory request"] = r.MemoryRequest
	labels["Memory usage"] = r.MemoryUsage
	labels["Total Milvus instance"] = r.InstanceTotal
	labels["creating"] = r.Creating
	labels["deleting"] = r.Deleting
	labels["healthy"] = r.Healthy
	labels["unhealthy"] = r.Unhealthy
	labels["8cu CPU request"] = r.CPUReauest8cu
	labels["8cu memory request"] = r.MemoryRequest8cu
	labels["Time"] = fmt.Sprintf(time.Now().Format("2006-01-02 15:04:05"))

	requestBody["alert"].(map[string]interface{})["alerts"].([]interface{})[0].(map[string]interface{})["status"] = r.ClusterName


	// title := "Cluster Resource Usage Details"
	// alert["annotations"].(map[string]interface{})["message"] = title

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
