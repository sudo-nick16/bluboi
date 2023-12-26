package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

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
	w *http.ResponseWriter
	r *http.Request
}

type SafeClients struct {
	mu sync.Mutex
	Clients []Client
}

func (sc *SafeClients) AddClient(client *Client) {
	sc.mu.Lock()
	sc.Clients = append(sc.Clients, *client)
	sc.mu.Unlock()
}

func (sc *SafeClients) RemoveClient(index int) {
	sc.mu.Lock()
	sc.Clients = append(sc.Clients[:index], sc.Clients[index+1:]...)
	sc.mu.Unlock()
}

func (sc *SafeClients) BroadcastLog(l []byte) {
	sc.mu.Lock()
	for _, client := range sc.Clients {
		w := (*client.w)
		_, err := w.Write(l)
		if err != nil {
			log.Printf("[ERROR] Could not write data in response - %v", err)
			return
		}
		if f, ok := w.(http.Flusher); ok {
			if f != nil {
				f.Flush()
			}
		}
	}
	sc.mu.Unlock()
}

var (
	Adapter = bluetooth.DefaultAdapter
	Logs = make(chan Log, 10)
	EventQueue = make(chan Event, 10)
	ConnectedDevice = Connection{}
	IsConnecting = false
	Devices = map[string]*bluetooth.Address{}
	Clients = SafeClients{}
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
		log.Printf("[INFO] Event: %v", e.Type)
		switch e.Type {
		case "SCAN" : {
			ctx, cancel := context.WithTimeout(context.TODO(), 5 * time.Second)
			defer cancel()
			LogInfo("Scanning...")
			for _, device := range Devices {
				LogDeviceInfo(device.String())
			}
			go func() {
				err := Adapter.Scan(func(adapter *bluetooth.Adapter, device bluetooth.ScanResult) {
					if device.LocalName() == "" {
						return
					}
					Devices[device.Address.String()] = &device.Address
					LogDeviceInfo(device.Address.String(), device.LocalName())
					select {
					case <-ctx.Done(): {
						EventQueue <- Event{ Type: "STOP_SCAN" }
						break;
					}
					default: {
						return;
					}
					}
					return
				})
				if err != nil {
					LogError("Could not scan for devices -", err.Error())
				}
			}()
			break
		}
		case "STOP_SCAN" : {
			LogInfo("Stopping Scanning...")
			err := Adapter.StopScan()
			if err != nil {
				LogError(err.Error())
				break;
			}
			LogInfo("Stopped.")
			break
		}
		case "CONNECT" : {
			if IsConnecting {
				LogError("Connection request is already in progress.")
				break
			}
			IsConnecting = true
			if Devices[e.Data] != nil {
				addr := *Devices[e.Data]
				dvc, err := Adapter.Connect(addr, bluetooth.ConnectionParams{
					ConnectionTimeout: bluetooth.NewDuration(5 * time.Second),
				})
				if err != nil {
					LogError("Could not connect to the device -", addr.String(), "-", err.Error())
					break
				}
				ConnectedDevice.BTDevice = dvc
				ConnectedDevice.Connected = true
				LogInfo("Connnected to -", addr.String())
			}
			go func () {
				err := Adapter.StopScan()
				if err != nil {
					log.Printf("[ERROR] Could not stop scanning - %v", err)
				}
				log.Println("[INFO] Stopped scanning.")
				LogInfo("Connecting to", e.Data)
				err = Adapter.Scan(func(adapter *bluetooth.Adapter, device bluetooth.ScanResult) {
					if device.LocalName() == "" {
						return
					}
					if device.Address.String() == e.Data {
						_ = Adapter.StopScan()
						dvc, err := Adapter.Connect(device.Address, bluetooth.ConnectionParams{
							ConnectionTimeout: bluetooth.NewDuration(5 * time.Second),
						})
						if err != nil {
							LogError("Could not connect to the device -", device.LocalName(), "-", err.Error())
							return
						}
						ConnectedDevice.BTDevice = dvc
						ConnectedDevice.Connected = true
						LogInfo("Connnected to -", device.LocalName())
					}
				})
				if err != nil {
					log.Printf("[ERROR] Could not scan for devices - %v", err.Error())
				}
				IsConnecting = false
			}()
			break
		}
		case "DISCONNECT" : {
			if IsConnecting {
				LogError("Active connection process is going on.")
				break
			}
			log.Println("CONNECT_DEVICE: ", ConnectedDevice)
			if ConnectedDevice.Connected == false {
				LogError("Not connected to a device.")
				break
			}
			LogInfo("Disconnecting...")
			err := ConnectedDevice.BTDevice.Disconnect()
			if err != nil {
				LogError("Could not disconnect -", err.Error())
			}
			LogInfo("Disconnected")
			ConnectedDevice = Connection{}
			break
		}
		default: {
			LogError("Invalid Event -", e.Type)
		}
		}
	}
}

func ServeUI() http.HandlerFunc {
	return func (w http.ResponseWriter, r *http.Request) {
		log.Printf("Received Request at public/%v %v", r.URL, r.Method)
		http.ServeFile(w, r, "public/" + strings.Trim(r.URL.Path, "/"))
		return
	}
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
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		index := len(Clients.Clients)
		Clients.AddClient(&Client{w: &w, r: r})
		select {
			case <-r.Context().Done():  {
				log.Printf("[INFO] Client Disconnected.")
				Clients.RemoveClient(index)
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
		Clients.BroadcastLog(LogToSSE(&l))
	}
}

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

	log.Println("hello there!")
}
