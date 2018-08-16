/*
 * INTEL CONFIDENTIAL
 * Copyright (2017) Intel Corporation.
 *
 * The source code contained or described herein and all documents related to the source code ("Material")
 * are owned by Intel Corporation or its suppliers or licensors. Title to the Material remains with
 * Intel Corporation or its suppliers and licensors. The Material may contain trade secrets and proprietary
 * and confidential information of Intel Corporation and its suppliers and licensors, and is protected by
 * worldwide copyright and trade secret laws and treaty provisions. No part of the Material may be used,
 * copied, reproduced, modified, published, uploaded, posted, transmitted, distributed, or disclosed in
 * any way without Intel/'s prior express written permission.
 * No license under any patent, copyright, trade secret or other intellectual property right is granted
 * to or conferred upon you by disclosure or delivery of the Materials, either expressly, by implication,
 * inducement, estoppel or otherwise. Any license under such intellectual property rights must be express
 * and approved by Intel in writing.
 * Unless otherwise agreed by Intel in writing, you may not remove or alter this notice or any other
 * notice embedded in Materials by Intel or Intel's suppliers or licensors in any way.
 */

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.impcloud.net/Responsive-Retail-Inventory/rfid-alert-service/app/alert"
	"github.impcloud.net/Responsive-Retail-Inventory/rfid-alert-service/app/config"
	"github.impcloud.net/Responsive-Retail-Inventory/rfid-alert-service/app/models"
)

func TestMain(m *testing.M) {
	if err := config.InitConfig(); err != nil {
		log.WithFields(log.Fields{
			"Method": "config.InitConfig",
			"Action": "Load config",
		}).Fatal(err.Error())
	}

	os.Exit(m.Run())
}

func TestProcessHeartbeat(t *testing.T) {
	notificationChan := make(chan alert.Notification, config.AppConfig.NotificationChanSize)
	inputData := mockGenerateHeartbeat()
	heartBeatError := processHeartbeat(&inputData, notificationChan)
	if heartBeatError != nil {
		t.Errorf("Error processing heartbeat %s", heartBeatError)
	}
	go alert.NotifyChannel(notificationChan)
}

func TestGatewayStatus(t *testing.T) {
	notificationChan := make(chan alert.Notification, config.AppConfig.NotificationChanSize)
	watchdogSeconds := 1
	//Starting gateway status check in separate goroutine
	go monitorHeartbeat(watchdogSeconds, notificationChan)
	missedHeartBeats := config.AppConfig.MaxMissedHeartbeats

	// check for gateway registered alert
	inputData := mockGenerateHeartbeat()
	heartBeatError := processHeartbeat(&inputData, notificationChan)
	if heartBeatError != nil {
		t.Errorf("Error processing heartbeat %s", heartBeatError)
	}
	if gateway.RegistrationStatus != models.Registered {
		t.Error("Failed to register gateway")
	}

	//Delay heartbeat by 3 seconds to check the functionality of missed heartbeat and gateway deregistered alert
	for i := 0; i <= missedHeartBeats; i++ {
		heartBeatError := processHeartbeat(&inputData, notificationChan)
		if heartBeatError != nil {
			t.Errorf("Error processing heartbeat %s", heartBeatError)
		}
		time.Sleep(2 * time.Second)
	}
	// looping through notification channel to make sure we get Gateway deregistred alert before checking for error conditions
	for noti := range notificationChan {
		if noti.NotificationMessage == "Gateway Deregistered Alert" {
			break
		}
	}
	if gateway.MissedHeartBeats != missedHeartBeats {
		t.Error("Failed to register missed heartbeats")
	}
	if gateway.RegistrationStatus != models.Deregistered {
		t.Error("Failed to deregister gateway")
	}
}

func TestHeartbeatAlert(t *testing.T) {
	input := mockGenerateHeartbeat()
	heartbeat, err := generateHeartbeatModel(input)
	if err != nil {
		t.Fatalf("Error generating heartbeat %s", err)
	}
	heartbeatAlert, deviceID := models.GatewayRegisteredAlert(heartbeat)
	if deviceID != heartbeat.DeviceID {
		t.Error("Alert device id does not match hearbeat device id")
	}
	if len(heartbeatAlert.Facilities) != len(heartbeat.Facilities) {
		t.Error("Number of alert facilitites does not match heartbeat facilities")
	}

	var heartbeatFacilities []string
	heartbeatFacilities = append(heartbeatFacilities, heartbeat.Facilities...)

	if !reflect.DeepEqual(heartbeatAlert.Facilities, heartbeatFacilities) {
		t.Error("Facilities from alert is not the same as heartbeat facilities")
	}

	input = mockGenerateHeartbeatNoFacility()
	heartbeat, err = generateHeartbeatModel(input)
	if err != nil {
		t.Fatalf("Error generating heartbeat with no facility %s", err)
	}
	heartbeatAlert, _ = models.GatewayRegisteredAlert(heartbeat)
	// Alert generated from heartbeat with no facilities should have facilities field with value "UNDEFINED_FACILITY"
	if len(heartbeatAlert.Facilities) != 1 {
		t.Errorf("Alert generated from heartbeat with no facilities should have a length of one")
	}
	if heartbeatAlert.Facilities[0] != models.UndefinedFacility {
		t.Errorf("Alert generated from heartbeat with no facilities has the wrong facility defined")
	}
}

func TestProcessShippingNoticeWRINs(t *testing.T) {
	notificationChan := make(chan alert.Notification, config.AppConfig.NotificationChanSize)
	testServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(1 * time.Second)
		if request.URL.EscapedPath() != "/skus" {
			t.Errorf("Expected request to '/skus', received %s", request.URL.EscapedPath())
		}
		var jsonData []byte
		if request.URL.EscapedPath() == "/skus" {
			result := buildProductData(0.0, 0.0, 0.0, 0.0, "00111111")
			jsonData, _ = json.Marshal(result)
		}

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(jsonData)
	}))

	defer testServer.Close()

	skuMapping := NewSkuMapping(testServer.URL + "/skus")

	config.AppConfig.EpcToWrin = true
	inputData := mockGenerateShippingNoticeWRINs()
	shippingError := skuMapping.processShippingNotice(&inputData, notificationChan)
	if shippingError != nil {
		t.Errorf("Error processing shipping notice %s", shippingError)
	}

}

func TestProcessShippingNoticeGTINs(t *testing.T) {
	notificationChan := make(chan alert.Notification, config.AppConfig.NotificationChanSize)
	testServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(1 * time.Second)
		if request.URL.EscapedPath() != "/skus" {
			t.Errorf("Expected request to '/skus', received %s", request.URL.EscapedPath())
		}
		var jsonData []byte
		if request.URL.EscapedPath() == "/skus" {
			result := buildProductData(0.0, 0.0, 0.0, 0.0, "00111111")
			jsonData, _ = json.Marshal(result)
		}

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(jsonData)
	}))

	defer testServer.Close()

	skuMapping := NewSkuMapping(testServer.URL + "/skus")
	config.AppConfig.EpcToWrin = false
	inputData := mockGenerateShippingNoticeGTINs()
	shippingError := skuMapping.processShippingNotice(&inputData, notificationChan)
	if shippingError != nil {
		t.Errorf("Error processing shipping notice %s", shippingError)
	}

}

func TestProcessShippingNoticeGTINsBadRequest(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
	}))

	defer testServer.Close()

	skuMapping := NewSkuMapping(testServer.URL + "/skus")
	config.AppConfig.EpcToWrin = false
	inputData := mockGenerateShippingNoticeGTINs()
	notificationChan := make(chan alert.Notification, config.AppConfig.NotificationChanSize)
	shippingError := skuMapping.processShippingNotice(&inputData, notificationChan)
	if shippingError == nil {
		t.Errorf("Expected error")
	}

}

func TestProcessShippingNoticeGTINMaxs(t *testing.T) {
	notificationChan := make(chan alert.Notification, config.AppConfig.NotificationChanSize)
	testServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(1 * time.Second)
		if request.URL.EscapedPath() != "/skus" {
			t.Errorf("Expected request to '/skus', received %s", request.URL.EscapedPath())
		}
		var jsonData []byte
		if request.URL.EscapedPath() == "/skus" {
			result := buildProductData(0.0, 0.0, 0.0, 0.0, "00111111")
			jsonData, _ = json.Marshal(result)
		}

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(jsonData)
	}))

	defer testServer.Close()

	skuMapping := NewSkuMapping(testServer.URL + "/skus")
	config.AppConfig.EpcToWrin = false
	config.AppConfig.BatchSizeMax = 1
	inputData := mockGenerateShippingNoticeGTINs()
	shippingError := skuMapping.processShippingNotice(&inputData, notificationChan)
	if shippingError != nil {
		t.Errorf("Error processing shipping notice %s", shippingError)
	}

}

func TestProcessShippingNoticeGTINMaxsBadRequest(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
	}))

	defer testServer.Close()

	skuMapping := NewSkuMapping(testServer.URL + "/skus")
	config.AppConfig.EpcToWrin = false
	config.AppConfig.BatchSizeMax = 1
	inputData := mockGenerateShippingNoticeGTINs()
	notificationChan := make(chan alert.Notification, config.AppConfig.NotificationChanSize)
	shippingError := skuMapping.processShippingNotice(&inputData, notificationChan)
	if shippingError == nil {
		t.Errorf("Expected error.")
	}

}

func TestMakeGetCallToSkuMappingWithError(t *testing.T) {
	skuMapping := NewSkuMapping("/skus")
	_, err := MakeGetCallToSkuMapping("", skuMapping.url)
	if err == nil {
		t.Errorf("Expected error.")
	}

}

func TestMakeGetCallToSkuMappingWithMashallError(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(1 * time.Second)
		if request.URL.EscapedPath() != "/skus" {
			t.Errorf("Expected request to '/skus', received %s", request.URL.EscapedPath())
		}
		var jsonData []byte
		if request.URL.EscapedPath() == "/skus" {
			//result := buildProductData(0.0, 0.0, 0.0, 0.0, "00111111")
			jsonData, _ = json.Marshal("this")
		}

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(jsonData)
	}))

	defer testServer.Close()

	skuMapping := NewSkuMapping(testServer.URL + "/skus")

	_, errorCall := MakeGetCallToSkuMapping("", skuMapping.url)
	if errorCall == nil {
		t.Errorf("Error expected")
	}

}

func mockGenerateHeartbeat() []byte {
	heartbeat := []byte(`{
		"macaddress": "02:42:ac:1d:00:04",
		"application": "rsp_collector",
		"providerId": -1,
		"dateTime": "2017-08-25T22:29:23.816Z",
		"type": "urn:x-intel:context:retailsensingplatform:heartbeat",
		"value": {
		  "device_id": "rrpgw",
		  "gateway_id": "rrpgw",
		  "facilities": [
				"facility1",
				"facility2"
		  ],
		  "facility_groups_cfg": "auto-0802233641",
		  "mesh_id": null,
		  "mesh_node_id": null,
		  "personality_groups_cfg": null,
		  "schedule_cfg": "UNKNOWN",
		  "schedule_groups_cfg": null,
		  "sent_on": 1503700192960
		}
	}`)
	return heartbeat
}

func mockGenerateShippingNoticeWRINs() []byte {
	shippingNotice := []byte(`{
  			"macaddress": "02:42:0a:00:1e:1a",
  			"application": "productmasterdataservicewithdropbox",
  			"providerId": -1,
  			"dateTime": "2018-07-30T19:07:10.461Z",
  			"type": "urn:x-intel:context:retailsensingplatform:shippingmasterdata",
  			"value": {
    			"data": [
      			{
        			"epc": "993402662C00000025871534"
      			},
      			{
        			"epc": "993402662C3A549106C00000"
      			},
      			{
        			"epc": "993402662C3A549107C00000"
      			},
      			{
        			"epc": "993402662C3A549105C00000"
      			},
      			{
        			"epc": "993402662C00000025871641"
      			}
    			]
  			}
	}`)
	return shippingNotice
}

func mockGenerateShippingNoticeGTINs() []byte {
	shippingNotice := []byte(`{
  			"macaddress": "02:42:0a:00:1e:1a",
  			"application": "productmasterdataservicewithdropbox",
  			"providerId": -1,
  			"dateTime": "2018-07-30T19:07:10.461Z",
  			"type": "urn:x-intel:context:retailsensingplatform:shippingmasterdata",
  			"value": {
    			"data": [
      			{
        			"epc": "30143639F84191AD22900204"
      			},
      			{
        			"epc": "30143639F84191AD66100207"
      			}
    			]
  			}
	}`)
	return shippingNotice
}

func mockGenerateHeartbeatNoFacility() []byte {
	heartbeat := []byte(`{
		"macaddress": "02:42:ac:1d:00:04",
		"application": "rsp_collector",
		"providerId": -1,
		"dateTime": "2017-08-25T22:29:23.816Z",
		"type": "urn:x-intel:context:retailsensingplatform:heartbeat",
		"value": {
		  "device_id": "rrpgw",
		  "gateway_id": "rrpgw",
		  "facilities": [],
		  "facility_groups_cfg": null,
		  "mesh_id": null,
		  "mesh_node_id": null,
		  "personality_groups_cfg": null,
		  "schedule_cfg": "UNKNOWN",
		  "schedule_groups_cfg": null,
		  "sent_on": 1503700192960
		}
	}`)
	return heartbeat
}

func generateHeartbeatModel(input []byte) (models.Heartbeat, error) {
	var heartbeatEvent models.HeartbeatMessage
	err := json.Unmarshal(input, &heartbeatEvent)
	if err != nil {
		log.Fatalf("error parsing Heartbeat: %s", err)
		return heartbeatEvent.Value, err
	}

	return heartbeatEvent.Value, nil
}

func buildProductData(becomingReadable float64, beingRead float64, dailyTurn float64, exitError float64, gtinSku string) models.SkuMappingResponse {
	var metadata = make(map[string]interface{})
	metadata["becoming_readable"] = becomingReadable
	metadata["being_read"] = beingRead
	metadata["daily_turn"] = dailyTurn
	metadata["exit_error"] = exitError

	gtinMetadata := models.GtinMetadata{
		Gtin: gtinSku,
	}

	gtinList := []models.GtinMetadata{gtinMetadata}

	var data = models.ProdData{
		GtinList: gtinList,
	}

	dataList := []models.ProdData{data}

	var result = models.SkuMappingResponse{
		ProdData: dataList,
	}
	return result
}
