package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"sync"
	"time"
)

const ProtocolVersion = 1

type Announcement struct {
	Version      int    `json:"version"`
	DeviceID     string `json:"deviceId"`
	DeviceName   string `json:"deviceName"`
	TransferPort int    `json:"transferPort"`
	Timestamp    int64  `json:"timestamp"`
}
type Peer struct {
	DeviceID     string    `json:"deviceId"`
	DeviceName   string    `json:"deviceName"`
	IP           string    `json:"ip"`
	TransferPort int       `json:"transferPort"`
	LastSeen     time.Time `json:"lastSeen"`
}
type Event struct {
	Type string
	Peer Peer
}
type Options struct {
	DeviceID     string
	DeviceName   string
	Port         int
	TransferPort int
	OnEvent      func(Event)
	OnReady      func()
}
type Service struct {
	options Options
	mutex   sync.RWMutex
	peers   map[string]Peer
}

func NewService(options Options) *Service {
	return &Service{options: options, peers: make(map[string]Peer)}
}
func (service *Service) Peers() []Peer {
	service.mutex.RLock()
	values := make([]Peer, 0, len(service.peers))
	for _, peer := range service.peers {
		values = append(values, peer)
	}
	service.mutex.RUnlock()
	sort.Slice(values, func(i, j int) bool { return values[i].DeviceName < values[j].DeviceName })
	return values
}
func (service *Service) Peer(id string) (Peer, bool) {
	service.mutex.RLock()
	defer service.mutex.RUnlock()
	value, ok := service.peers[id]
	return value, ok
}

func (service *Service) Start(ctx context.Context) error {
	address := &net.UDPAddr{IP: net.IPv4zero, Port: service.options.Port}
	connection, err := net.ListenUDP("udp4", address)
	if err != nil {
		return err
	}
	defer connection.Close()
	if service.options.OnReady != nil {
		service.options.OnReady()
	}
	go service.broadcast(ctx, connection)
	go service.expire(ctx)
	buffer := make([]byte, 64*1024)
	for {
		_ = connection.SetReadDeadline(time.Now().Add(time.Second))
		count, remote, err := connection.ReadFromUDP(buffer)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				continue
			}
			return err
		}
		var announcement Announcement
		if json.Unmarshal(buffer[:count], &announcement) != nil || announcement.Version != ProtocolVersion || announcement.DeviceID == "" || announcement.DeviceID == service.options.DeviceID || announcement.TransferPort < 1 || announcement.TransferPort > 65535 {
			continue
		}
		peer := Peer{DeviceID: announcement.DeviceID, DeviceName: announcement.DeviceName, IP: remote.IP.String(), TransferPort: announcement.TransferPort, LastSeen: time.Now()}
		service.mutex.Lock()
		_, exists := service.peers[peer.DeviceID]
		service.peers[peer.DeviceID] = peer
		service.mutex.Unlock()
		if !exists && service.options.OnEvent != nil {
			service.options.OnEvent(Event{Type: "online", Peer: peer})
		}
	}
}

func (service *Service) broadcast(ctx context.Context, connection *net.UDPConn) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	target := &net.UDPAddr{IP: net.IPv4bcast, Port: service.options.Port}
	send := func() {
		data, _ := json.Marshal(Announcement{Version: ProtocolVersion, DeviceID: service.options.DeviceID, DeviceName: service.options.DeviceName, TransferPort: service.options.TransferPort, Timestamp: time.Now().Unix()})
		_, _ = connection.WriteToUDP(data, target)
	}
	send()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			send()
		}
	}
}
func (service *Service) expire(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			var expired []Peer
			service.mutex.Lock()
			for id, peer := range service.peers {
				if now.Sub(peer.LastSeen) > 7*time.Second {
					expired = append(expired, peer)
					delete(service.peers, id)
				}
			}
			service.mutex.Unlock()
			if service.options.OnEvent != nil {
				for _, peer := range expired {
					service.options.OnEvent(Event{Type: "offline", Peer: peer})
				}
			}
		}
	}
}
