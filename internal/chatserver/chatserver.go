package chatserver

import (
	"fmt"
	"sync"
	"time"

	"distributed-chat-coordinator/internal/types"
)

type ChatRoom struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`

	mu       sync.Mutex
	messages []ChatMessage
	members  map[string]bool
}

type ChatMessage struct {
	Sender    string    `json:"sender"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

func NewChatRoom(name string) *ChatRoom {
	return &ChatRoom{
		Name:      name,
		CreatedAt: time.Now(),
		messages:  make([]ChatMessage, 0),
		members:   make(map[string]bool),
	}
}

func (r *ChatRoom) AddMessage(sender, content string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, ChatMessage{
		Sender:    sender,
		Content:   content,
		Timestamp: time.Now(),
	})
}

func (r *ChatRoom) Join(memberID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.members[memberID] = true
}

func (r *ChatRoom) GetMessages() []ChatMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := make([]ChatMessage, len(r.messages))
	copy(copied, r.messages)
	return copied
}

type ChatServer struct {
	mu sync.Mutex

	ID     string
	Addr   string
	prefix string

	rooms map[string]*ChatRoom

	clients map[string]bool
}

func NewChatServer(id, addr string) *ChatServer {
	nodeNum := 0
	if len(id) > 0 {
		nodeNum = int(id[len(id)-1]) % 6
	}
	return &ChatServer{
		ID:      id,
		Addr:    addr,
		prefix:  types.LogPrefix(nodeNum, "CHAT-SRV-"+id),
		rooms:   make(map[string]*ChatRoom),
		clients: make(map[string]bool),
	}
}

func (s *ChatServer) CreateRoom(name string) *ChatRoom {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.rooms[name]; ok {
		fmt.Printf("%s⚠️  Room \"%s\" already exists on this server\n", s.prefix, name)
		return existing
	}

	room := NewChatRoom(name)
	s.rooms[name] = room

	fmt.Printf("%s🏠 Created room \"%s\"\n", s.prefix, name)
	return room
}

func (s *ChatServer) GetRoom(name string) *ChatRoom {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rooms[name]
}

func (s *ChatServer) ListRooms() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(s.rooms))
	for name := range s.rooms {
		names = append(names, name)
	}
	return names
}

func (s *ChatServer) ConnectClient(clientID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[clientID] = true
	fmt.Printf("%s👤 Client \"%s\" connected\n", s.prefix, clientID)
}

func (s *ChatServer) SendMessage(roomName, sender, content string) bool {
	s.mu.Lock()
	room, ok := s.rooms[roomName]
	s.mu.Unlock()

	if !ok {
		fmt.Printf("%s⚠️  Room \"%s\" not found on this server\n", s.prefix, roomName)
		return false
	}

	room.AddMessage(sender, content)
	fmt.Printf("%s💬 [%s] %s: %s\n", s.prefix, roomName, sender, content)
	return true
}
