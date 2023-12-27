package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"tinygo.org/x/bluetooth"
)

type Event struct {
	Type string
	Data string
}

type Log struct {
	Level string
	Msg string
}

type Connection struct {
	BTDevice *bluetooth.Device
	Connected bool
}

type Client struct {
	id uint32
	w http.ResponseWriter
	r *http.Request
}

type SafeClients struct {
	mu sync.Mutex
	Clients []Client
}

func (sc *SafeClients) Flush(index int) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if f, ok := sc.Clients[index].w.(http.Flusher); ok {
		f.Flush()
	}
}

func (sc *SafeClients) AddClient(client Client) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.Clients = append(sc.Clients, client)
}

func (sc *SafeClients) Length() int {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return len(sc.Clients)
}

func (sc *SafeClients) RemoveClient(id uint32) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for i, c := range sc.Clients {
		if c.id == id {
			sc.Clients = append(sc.Clients[:i], sc.Clients[i+1:]...)
			break
		}
	}
}

func (sc *SafeClients) BroadcastLog(l []byte) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for i, client := range sc.Clients {
		w := client.w
		_, err := w.Write(l)
		if err != nil {
			log.Printf("[ERROR] Could not write data in response - %v", err)
			sc.Clients = append(sc.Clients[:i], sc.Clients[i+1:]...)
			continue
		}
		select {
		case <- client.r.Context().Done(): {
			sc.Clients = append(sc.Clients[:i], sc.Clients[i+1:]...)
			break
		}
		default: {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		}
	}
}

type Device struct {
	Name string
	Address *bluetooth.Address
}

type SafeDevices struct {
	mu sync.Mutex
	Devices map[string] Device
}

func (sd *SafeDevices) ForEach(callback func (key string, value Device)) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	for addr, device := range sd.Devices {
		callback(addr, device)
	}
}

func (sd *SafeDevices) Device(addr string) Device {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.Devices[addr]
}

func (sd *SafeDevices) Exists(addr string) bool {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	if _, ok := sd.Devices[addr]; ok {
		return true
	}
	return false
}

func (sd *SafeDevices) Add(device Device) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	sd.Devices[device.Address.String()] = device
}


type SafeAdapter struct {
	mu sync.Mutex
	Adapter *bluetooth.Adapter
	BTDevice *bluetooth.Device
	Connected bool
}

func (sa *SafeAdapter) Enable() error {
	// sa.mu.Lock()
	// defer sa.mu.Unlock()
	err := sa.Adapter.Enable()
	return err
}

func (sa *SafeAdapter) Connect(address string) {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	if sa.Connected || sa.BTDevice != nil {
		LogError("You're already connected.")
		return
	}
	if !Devices.Exists(address) {
		LogError("Could not find the device.")
		return
	}
	device := Devices.Device(address)
	dvc, err := sa.Adapter.Connect(*device.Address, bluetooth.ConnectionParams{})
	if err != nil {
		LogError("Could not connect to ", device.Name, err.Error())
		return
	}
	sa.BTDevice = dvc
	sa.Connected = true
	LogInfo("Connected to", device.Name)
}

func (sa *SafeAdapter) Scan(seconds time.Duration) {
	// sa.mu.Lock()
	// defer sa.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.TODO(), seconds * time.Second)
	defer cancel()
	LogInfo("Scanning...")
	go func () {
		err := sa.Adapter.Scan(func (b *bluetooth.Adapter, result bluetooth.ScanResult) {
			if result.LocalName() == "" {
				return
			}
			if Devices.Exists(result.Address.String()) {
				return
			}
			Devices.Add(Device {
				Name: result.LocalName(),
				Address: &result.Address,
			})
			LogDeviceInfo(result.Address.String(), result.LocalName())
		})
		if err != nil {
			LogError(err.Error())
		}
	} ()
	for {
		select {
			case <-ctx.Done(): {
				err := sa.Adapter.StopScan()
				if err != nil {
					log.Printf("[ERROR] Could not stop scanning after timeout - %v", err)
					return
				}
				LogInfo("Stopped Scanning.")
				return
			}
			default: 
		}
	}
}

func (sa *SafeAdapter) StopScan() {
	// sa.mu.Lock()
	// defer sa.mu.Unlock()
	err := sa.Adapter.StopScan()
	if err != nil {
		LogError("Could not stop scanning -", err.Error())
		return
	}
	LogInfo("Stopped Scanning.")
}

func (sa *SafeAdapter) Disconnect() {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	if !sa.Connected || sa.BTDevice == nil {
		LogError("Currently not connected to any device.")
		return;
	}
	err := sa.BTDevice.Disconnect()
	if err != nil {
		LogError("Could not disconnect device -", err.Error())
		return;
	}
	sa.Connected = false
	sa.BTDevice = nil
	LogInfo("Disconnected.")
}

var (
	Adapter = SafeAdapter{Adapter: bluetooth.DefaultAdapter, BTDevice: nil}
	Logs = make(chan Log, 10)
	EventQueue = make(chan Event, 10)
	ConnectedDevice = Connection{}
	IsConnecting = false
	Devices = SafeDevices{Devices: map[string]Device{}}
	Clients = SafeClients{Clients: []Client{}}
)

func LogInfo(info ...string) {
	Logs <- Log {
		Level: "INFO",
		Msg: strings.Join(info, " "),
	}
}

func LogDeviceInfo(info ...string) {
	Logs <- Log {
		Level: "DEVICE",
		Msg: strings.Join(info, ";"),
	}
}
 
func LogError(err ...string) {
	Logs <- Log {
		Level: "ERROR",
		Msg: strings.Join(err, " "),
	}
}

func LogToSSE(l *Log) []byte {
	return []byte("event: " + l.Level + "\ndata: \"" + l.Msg + "\"\n\n")
}

func ProcessEventQueue() {
	log.Printf("[INFO] Consuming Bluetooth Events.")
	for {
		e := <-EventQueue
		log.Printf("[INFO] Received Event: %v", e.Type)
		switch e.Type {
		case "SCAN" : {
			go Adapter.Scan(5)
			break
		}
		case "STOP_SCAN" : {
			go Adapter.StopScan()
			break
		}
		case "CONNECT" : {
			go Adapter.Connect(e.Data)
			break
		}
		case "DISCONNECT" : {
			go Adapter.Disconnect()
			break
		}
		}
	}
}

func ServeUI() http.Handler {
	fsys, err := fs.Sub(public, "public")
    if err != nil {
        log.Fatalf("Could not read filesystem - %v", err)
    }
    return http.FileServer(http.FS(fsys))
}

func ScanHandler() http.HandlerFunc {
	return func (w http.ResponseWriter, r *http.Request) {
		EventQueue <- Event {
			Type: "SCAN",
		}
		w.WriteHeader(200)
	}
}

func GetEventsHandler() http.HandlerFunc {
	return func (w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		for len(Logs) > 0 {
			<-Logs
		}
		for len(EventQueue) > 0 {
			<-EventQueue
		}
		id := uuid.New().ID()
		index := Clients.Length()
		Clients.AddClient(Client{id, w, r})
		Clients.Flush(index)
		Devices.ForEach(func (_ string, device Device) {
			LogDeviceInfo(device.Address.String(), device.Name)
		})
		select {
			case <-r.Context().Done():  {
				log.Printf("[INFO] Client Disconnected.")
				Clients.RemoveClient(id)
				break
			}
		}
	}
}

func StopScanHandler() http.HandlerFunc {
	return func (w http.ResponseWriter, r *http.Request) {
		EventQueue <- Event {
			Type: "STOP_SCAN",
		}
		w.WriteHeader(200)
	}
}

func ConnectHandler() http.HandlerFunc {
	return func (w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		EventQueue <- Event {
			Type: "CONNECT",
			Data: vars["addr"],
		}
		w.WriteHeader(200)
	}
}

func DisconnectHandler() http.HandlerFunc {
	return func (w http.ResponseWriter, r *http.Request) {
		EventQueue <- Event {
			Type: "DISCONNECT",
		}
		w.WriteHeader(200)
	}
}

func BroadcastLogs() {
	for {
		l := <-Logs
		go Clients.BroadcastLog(LogToSSE(&l))
	}
}

//go:embed public/*
var public embed.FS

func main() {
	err := Adapter.Enable() 
	if err != nil {
		log.Fatalf("[ERROR] Could not enable bluetooth - %v", err)
	}	
	go ProcessEventQueue()
	go BroadcastLogs()

	log.Println("[INFO] Starting HTTP server")
	r := mux.NewRouter()
	r.Handle("/events", GetEventsHandler())
	r.Handle("/scan", ScanHandler())
	r.Handle("/stop", StopScanHandler())
	r.Handle("/connect/{addr}", ConnectHandler())
	r.Handle("/disconnect", DisconnectHandler())
	r.PathPrefix("/").Handler(ServeUI())
	server := http.Server {
		Addr: ":6969",
		Handler: r,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 10 * time.Second,
	}
	err = server.ListenAndServe()
	if err != nil {
		log.Printf("[ERROR] Could not start the server - %v", err)
	}
}
