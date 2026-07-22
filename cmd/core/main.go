package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"easyshare/internal/api"
	"easyshare/internal/cloud"
	"easyshare/internal/cloud/objectstore/s3store"
	"easyshare/internal/cloud/webdavfs"
	"easyshare/internal/config"
	"easyshare/internal/desktop"
	"easyshare/internal/discovery"
	"easyshare/internal/drive"
	"easyshare/internal/logging"
	"easyshare/internal/task"
	"easyshare/internal/transfer"
)

func main() {
	if file, path, err := logging.Open("core.log"); err == nil {
		defer file.Close()
		log.SetOutput(file)
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
		log.Printf("Core starting; log=%s", path)
	} else {
		log.Printf("open runtime log: %v", err)
	}
	defer log.Printf("Core stopped")

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancelCore := context.WithCancel(signalCtx)
	defer cancelCore()
	defaultPath := config.DefaultConfigPath()
	configPath := flag.String("config", defaultPath, "path to the EasyShare configuration")
	flag.Parse()
	value, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	baseURL := "http://" + net.JoinHostPort(value.APIHost, strconv.Itoa(value.APIPort))
	processOptions := desktop.ProcessOptions{BaseURL: baseURL, Token: value.APIToken, DeviceID: value.DeviceID}
	if desktop.CoreHealthy(processOptions) {
		log.Printf("compatible Core already running at %s; exiting duplicate process", baseURL)
		return
	}
	taskStore := task.NewStore()
	taskStore.EnablePersistence(filepath.Join(filepath.Dir(*configPath), "history.json"))
	server := api.NewServer(value, taskStore)
	server.ConfigureDrive(drive.NewService(value.WebDAVRoot))
	server.ConfigureShutdown(cancelCore)
	server.ConfigureConfigPath(*configPath)
	// 局域网共享 WebDAV 随 Core 自动启动，"此电脑"入口始终可用。
	server.StartLANDrive()
	// Cloud drive uses fixed build-time parameters; no user configuration needed.
	cloudStore, cloudErr := s3store.New(s3store.Config{
		Endpoint:          cloud.DefaultEndpoint,
		Region:            cloud.DefaultRegion,
		AccessKeyID:       cloud.DefaultAccessKeyID,
		SecretAccessKey:   cloud.DefaultSecretAccessKey,
		AllowInsecureHTTP: cloud.DefaultAllowInsecureHTTP,
	})
	if cloudErr != nil {
		log.Printf("cloud drive disabled: %v", cloudErr)
	} else {
		server.ConfigureCloud(cloud.NewService(cloudStore, cloud.DefaultBucket))
		// Expose cloud files via WebDAV so Windows Explorer can browse them.
		cloudFS := webdavfs.New(cloudStore, cloud.DefaultBucket)
		cloudDriveService := drive.NewCloudService(cloudFS)
		server.ConfigureCloudDrive(cloudDriveService)
		server.StartCloudDrive(ctx)
		log.Printf("cloud drive enabled: endpoint=%s bucket=%s", cloud.DefaultEndpoint, cloud.DefaultBucket)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("core cleanup: %v", err)
		}
	}()
	discoveryService := discovery.NewService(discovery.Options{DeviceID: value.DeviceID, DeviceName: value.DeviceName, Port: value.DiscoveryPort, TransferPort: value.TransferPort, OnReady: func() { server.MarkDiscovery(true) }, OnEvent: func(event discovery.Event) {
		server.Publish(api.Event{Type: "peer." + event.Type, Data: event.Peer})
	}})
	server.ConfigureDiscovery(discoveryService)
	receiver := transfer.NewReceiver(transfer.ReceiverOptions{Host: "0.0.0.0", Port: value.TransferPort, ReceiveDir: value.ReceiveDir, Tasks: taskStore, OnReady: func() { server.MarkReceiver(true) }, OnUpdate: func(value task.Task) { server.Publish(api.Event{Type: "transfer.updated", Data: value}) }})
	server.ConfigureTransfer(receiver)
	go func() {
		if err := receiver.Start(ctx); err != nil {
			server.MarkReceiver(false)
			log.Printf("receiver stopped: %v", err)
		}
	}()
	go func() {
		if err := discoveryService.Start(ctx); err != nil {
			server.MarkDiscovery(false)
			log.Printf("discovery stopped: %v", err)
		}
	}()
	log.Printf("easyshare core listening on %s:%d", value.APIHost, value.APIPort)
	if err := server.Start(ctx); err != nil {
		// Two desktop launches can race between the initial health probe and
		// binding the API port. Treat a compatible winner as success instead
		// of emitting a misleading fatal port-conflict error.
		if desktop.CoreHealthy(processOptions) {
			log.Printf("compatible Core won startup race at %s; exiting duplicate process", baseURL)
			return
		}
		log.Fatal(err)
	}
}
