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

package alert

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	metrics "github.impcloud.net/Responsive-Retail-Core/utilities/go-metrics"
	"github.impcloud.net/Responsive-Retail-Inventory/rfid-alert-service/app/config"
	"github.impcloud.net/Responsive-Retail-Inventory/rfid-alert-service/app/models"
)

const (
	// AlertType is type for alert
	AlertType = "Alert"
	//
	jsonApplication = "application/json;charset=utf-8"
	//
	connectionTimeout = 15
	// Not Whitelisted Alert Type
	NotWhitelisted = 401
)

// ProcessAlert takes alert json bytes and post to notification channel
func ProcessAlert(jsonBytes *[]byte, notificationChan chan Notification) error {
	// Metrics
	metrics.GetOrRegisterGauge("RFID-Alert.ProcessAlert.Attempt", nil).Update(1)
	startTime := time.Now()
	defer metrics.GetOrRegisterTimer("RFID-Alert.ProcessAlert.Latency", nil).UpdateSince(startTime)
	mSuccess := metrics.GetOrRegisterGauge("RFID-Alert.ProcessAlert.Success", nil)
	mUnmarshalErr := metrics.GetOrRegisterGauge("RFID-Alert.ProcessAlert.Unmarshal-Error", nil)

	jsoned := string(*jsonBytes)
	log.Infof("Received alert:\n%s", jsoned)

	var data map[string]interface{}

	var gatewayID string
	if err := json.Unmarshal(*jsonBytes, &data); err != nil {
		log.Errorf("error parsing Alert %s", err)
		mUnmarshalErr.Update(1)
		return err
	}
	if value, ok := data["value"].(map[string]interface{}); !ok {
		return errors.New("Type assertion failed")
	} else {
		gatewayID, ok = value["gateway_id"].(string)
		if !ok {
			// ASN Alert will not contain gateway id
			log.Warn("This may not be an issue, but received Alert without gateway id.")
		}
	}

	var alertEvent models.AlertMessage
	err := json.Unmarshal(*jsonBytes, &alertEvent)
	if err != nil {
		log.Errorf("error parsing Alert %s", err)
		mUnmarshalErr.Update(1)
		return err
	}
	go func() {
		notificationChan <- Notification{
			NotificationMessage: "Process Alert",
			NotificationType:    AlertType,
			Data:                alertEvent.Value,
			GatewayID:           gatewayID,
		}
	}()

	log.Info("Processed alert")
	mSuccess.Update(1)
	return nil
}

// NotifyChannel iterates through messages in the notification channel and post to cloud connector
func NotifyChannel(notificationChan chan Notification) {
	// CloudConnector URL to send alerts
	cloudConnector := config.AppConfig.CloudConnectorURL + config.AppConfig.CloudConnectorEndpoint
	notificationChanSize := config.AppConfig.NotificationChanSize

	for notification := range notificationChan {
		if len(notificationChan) >= notificationChanSize-10 {
			log.WithFields(log.Fields{
				"notificationChanSize": len(notificationChan),
				"maxChannelSize":       notificationChanSize,
			}).Warn("Channel size getting full!")
		}
		generateErr := notification.GeneratePayload()
		if generateErr != nil {
			log.Errorf("Problem generating payload for %s, %s", notification.NotificationType, generateErr)
		} else {
			_, err := PostNotification(notification.Data, cloudConnector)
			if err != nil {
				log.Errorf("Problem sending notification for %s, %s", notification.NotificationMessage, err)
			}
		}
	}
}

// PostNotification post notification data vial http call to the toURL
func PostNotification(data interface{}, toURL string) ([]byte, error) {
	// Metrics
	metrics.GetOrRegisterGauge("RFID-Alert.PostNotification.Attempt", nil).Update(1)
	startTime := time.Now()
	defer metrics.GetOrRegisterTimer("RFID-Alert.PostNotification.Latency", nil).UpdateSince(startTime)
	mSuccess := metrics.GetOrRegisterGauge("RFID-Alert.PostNotification.Success", nil)
	mMarshalErr := metrics.GetOrRegisterGauge("RFID-Alert.PostNotification.Marshal-Error", nil)
	mNotifyErr := metrics.GetOrRegisterGauge("RFID-Alert.PostNotification.Notify-Error", nil)

	timeout := time.Duration(connectionTimeout) * time.Second
	client := &http.Client{
		Timeout: timeout,
	}

	mData, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		mMarshalErr.Update(1)
		return nil, errors.Errorf("Payload Marshalling failed  %v", err)
	}
	log.Debugf("Payload to cloud-connector after marshalling:\n%s", string(mData))
	request, reqErr := http.NewRequest("POST", toURL, bytes.NewBuffer(mData))
	if reqErr != nil {
		return nil, reqErr
	}
	request.Header.Set("content-type", jsonApplication)
	response, respErr := client.Do(request)
	if respErr != nil {
		mNotifyErr.Update(1)
		return nil, respErr
	}

	if response.StatusCode != http.StatusOK {
		mNotifyErr.Update(1)
		return nil, errors.Errorf("PostNotification failed with following response code %d", response.StatusCode)

	}

	var responseData []byte
	if response.Body != nil {
		responseData, err = ioutil.ReadAll(response.Body)
		if err != nil {
			return nil, errors.Wrapf(err, "unable to ReadALL response.Body")
		}
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			log.WithFields(log.Fields{
				"Method": "postNotification",
				"Action": "response.Body.Close()",
			}).Info(err.Error())
		}
	}()

	log.Debug("Notification posted")
	mSuccess.Update(1)
	return responseData, nil
}