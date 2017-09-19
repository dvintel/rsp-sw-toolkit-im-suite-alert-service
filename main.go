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
	"bytes"
	"context"
	"context_linux_go/core"
	"context_linux_go/core/sensing"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.impcloud.net/Responsive-Retail-MVP/rfid-alert-service/app/config"
	"github.impcloud.net/Responsive-Retail-MVP/rfid-alert-service/app/routes"
)

const (
	eventsUrn    = "urn:x-intel:context:retailsensingplatform:events"
	heartbeatUrn = "urn:x-intel:context:retailsensingplatform:heartbeat"
	alertsUrn    = "urn:x-intel:context:retailsensingplatform:alerts"
)

// initConfig will initialize our configuration parameters
func initConfig() {
	// Load config variables
	if err := config.InitConfig(); err != nil {
		log.WithFields(log.Fields{
			"Method": "config.InitConfig",
			"Action": "Load config",
		}).Fatal(err.Error())
	}

	if config.AppConfig.LoggingLevel == "debug" {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetFormatter(&log.JSONFormatter{})
	}
}

// nolint: gocyclo
func initSensing() {
	onSensingStarted := make(core.SensingStartedChannel)
	onSensingError := make(core.ErrorChannel)

	sensingOptions := core.SensingOptions{
		Server:        config.AppConfig.ContextSensing,
		Publish:       true,
		Secure:        false,
		Application:   config.AppConfig.ServiceName,
		OnStarted:     onSensingStarted,
		OnError:       onSensingError,
		Retries:       10,
		RetryInterval: 1,
	}

	sensingSdk := sensing.NewSensing()
	sensingSdk.Start(sensingOptions)

	go func(options core.SensingOptions) {
		onHeartbeat := make(core.ProviderItemChannel)
		onEvent := make(core.ProviderItemChannel)
		onAlert := make(core.ProviderItemChannel)

		for {
			select {
			case started := <-options.OnStarted:
				if !started.Started {
					log.WithFields(log.Fields{
						"Method": "main",
						"Action": "connecting to context broker",
						"Host":   config.AppConfig.ContextSensing,
					}).Fatal("sensing has failed to start")
				}

				log.Info("Sensing has started")
				sensingSdk.AddContextTypeListener("*:*", heartbeatUrn, &onHeartbeat, &onSensingError)
				sensingSdk.AddContextTypeListener("*:*", eventsUrn, &onEvent, &onSensingError)
				sensingSdk.AddContextTypeListener("*:*", alertsUrn, &onAlert, &onSensingError)
				log.Info("Waiting for Heartbeat, Event, and Alert data....")

			case heartbeat := <-onHeartbeat:
				jsonBytes, err := json.MarshalIndent(*heartbeat, "", "  ")
				if err != nil {
					log.Errorf("Unable to process heartbeat")
				}

				if err := processHeartbeat(&jsonBytes); err != nil {
					log.WithFields(log.Fields{
						"Method": "main",
						"Action": "process HeartBeat",
						"Error":  err.Error(),
					}).Error("error processing heartbeat data")
				}
			case event := <-onEvent:
				var err error
				jsonBytes, err := json.MarshalIndent(*event, "", "  ")
				if err != nil {
					log.Errorf("Unable to process event")
				}
				if err := processEvent(&jsonBytes); err != nil {
					log.WithFields(log.Fields{
						"Method": "main",
						"Action": "process Event",
						"Error":  err.Error(),
					}).Error("error processing event")
				}
			case alert := <-onAlert:
				var err error
				jsonBytes, err := json.MarshalIndent(*alert, "", "  ")
				if err != nil {
					log.Errorf("Unable to process alert")
				}
				if err := processAlert(&jsonBytes); err != nil {
					log.WithFields(log.Fields{
						"Method": "main",
						"Action": "process Alert",
						"Error":  err.Error(),
					}).Error("error processing alert")
				}
			case err := <-options.OnError:
				log.Errorf("Received sensingSdk error: %v", err)
			}
		}
	}(sensingOptions)
}

func processHeartbeat(jsonBytes *[]byte) error {
	jsoned := string(*jsonBytes)
	log.Infof("Received Heartbeat:\n%s", jsoned)

	heartbeatEndpoint := config.AppConfig.SendHeartbeatTo
	if err := postNotification(jsoned, heartbeatEndpoint); err != nil {
		log.Errorf("Problem sending Heartbeat: %s", err)
		return err
	}

	log.Infof("Sent Heartbeat to %s", heartbeatEndpoint)
	return nil
}

func processEvent(jsonBytes *[]byte) error {
	jsoned := string(*jsonBytes)
	log.Infof("Received event:\n%s", jsoned)
	eventEndpoint := config.AppConfig.SendEventTo
	if err := postNotification(jsoned, eventEndpoint); err != nil {
		log.Errorf("Problem sending Event: %s", err)
		return err
	}

	log.Infof("Sent event to %s", eventEndpoint)
	return nil
}

func processAlert(jsonBytes *[]byte) error {
	jsoned := string(*jsonBytes)
	log.Infof("Received alert:\n%s", jsoned)
	eventEndpoint := config.AppConfig.SendAlertTo
	if err := postNotification(jsoned, eventEndpoint); err != nil {
		log.Errorf("Problem sending Event: %s", err)
		return err
	}

	log.Infof("Sent alert to %s", eventEndpoint)
	return nil
}

const (
	jsonApplication   = "application/json;charset=utf-8"
	connectionTimeout = 15
)

func postNotification(data interface{}, to string) error {
	timeout := time.Duration(connectionTimeout) * time.Second
	client := &http.Client{
		Timeout: timeout,
	}

	mData, err := json.Marshal(data)
	if err == nil {
		request, _ := http.NewRequest("POST", to, bytes.NewBuffer(mData))
		request.Header.Set("content-type", jsonApplication)
		response, err := client.Do(request)
		if err != nil ||
			response.StatusCode != http.StatusOK {
			return err
		}
		defer func() {
			if err := response.Body.Close(); err != nil {
				log.WithFields(log.Fields{
					"Method": "postNotification",
					"Action": "post notification to notification service",
				}).Info(err.Error())
			}
		}()
	}
	return nil
}

func main() {

	initConfig()

	log.WithFields(log.Fields{
		"Method": "main",
		"Action": "Start",
	}).Info("Starting application...")

	initSensing()

	// Start Webserver
	router := routes.NewRouter()

	// Create a new server and set timeout values.
	server := http.Server{
		Addr:           ":8080",
		Handler:        router,
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// We want to report the listener is closed.
	var wg sync.WaitGroup
	wg.Add(1)

	// Start the listener.
	go func() {
		log.Infof("%s running", config.AppConfig.ServiceName)
		log.Infof("Listener closed : %v", server.ListenAndServe())
		wg.Done()
	}()

	// Listen for an interrupt signal from the OS.
	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt)

	// Wait for a signal to shutdown.
	<-osSignals

	// Create a context to attempt a graceful 5 second shutdown.
	const timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Attempt the graceful shutdown by closing the listener and
	// completing all inflight requests.
	if err := server.Shutdown(ctx); err != nil {
		log.WithFields(log.Fields{
			"Method":  "main",
			"Action":  "shutdown",
			"Timeout": timeout,
			"Message": err.Error(),
		}).Error("Graceful shutdown did not complete")

		// Looks like we timed out on the graceful shutdown, Force Kill
		if err := server.Close(); err != nil {
			log.WithFields(log.Fields{
				"Method":  "main",
				"Action":  "shutdown",
				"Message": err.Error(),
			}).Error("Error killing server")
		}
	}

	// Wait for the listener to report it is closed.
	wg.Wait()
	log.WithField("Method", "main").Info("Completed.")
}