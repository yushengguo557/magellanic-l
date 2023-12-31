package ws

import (
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"io"
	"log"
	"sync"
)

const (
	ExchangeName = "websocket-messages-router"
	ExchangeType = "direct"

	QueueName = "websocket-messages-queue"
)

// WebSocketManager websocket管理器
type WebSocketManager struct {
	ID                string
	Clients           map[string]*Client
	Channels          map[string]*Channel
	Messages          chan Message      // 消息处理通道
	MessageQueue      MessageQueue      // 当前服务器独占的消息队列 用于接收来自其他服务器发送的 websocket 消息
	ClientToServerMap ClientToServerMap // 客户端 到 管理器的映射 服务于消息队列
	Lock              sync.RWMutex
}

// NewWebSocketManager 实例化websocket管理器
// cap: 消息通道容量
func NewWebSocketManager(id string, cap int, rdb *redis.Client, mq MessageQueue) *WebSocketManager {
	return &WebSocketManager{
		ID:                id,
		Clients:           make(map[string]*Client),
		Channels:          make(map[string]*Channel),
		Messages:          make(chan Message, cap),
		MessageQueue:      mq,
		ClientToServerMap: ClientToServerMap{rdb},
	}
}

// Register 使用 WebSocketManager 对 client 进行管理 & 接收客户端发送过来的所有消息
func (m *WebSocketManager) Register(client *Client) {
	var err error
	var msg Message

	// 1.添加 client -> manager id 映射 & client uid -> client 映射
	err = m.ClientToServerMap.Set(client.UID, m.ID)
	if err != nil {
		log.Panic(err)
	}
	m.Clients[client.UID] = client

	fmt.Printf("----------------client [%s] register successfully----------------\n", client.UID)
	// 2.读取来自客户端的消息 & 进行分发 (发布到消息队列 or 当前管理器的消息通道)
	for {
		// fmt.Printf("online population: %d\r", len(m.Clients))
		msg, err = client.Read()
		// 退出循环
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Println("read data when registering, err:", err)
			}
			break
		}

		m.Messages <- msg
	}
}

// Logout 取消 WebSocketManager 对 client 的管理 & 从所有频道中移除该客户端
func (m *WebSocketManager) Logout(uid string) {
	// 1.删除映射
	m.ClientToServerMap.Del(uid)

	// 2.关闭连接
	if m.IsManaged(uid) {
		m.Clients[uid].Conn.Close()
	} else {
		log.Printf("client [%s] has closed\n", uid)
	}

	// 3.移除管理
	delete(m.Clients, uid)

	// 4.移出频道
	for _, ch := range m.Channels {
		delete(ch.Members, uid)
	}

	fmt.Printf("----------------client [%s] logout successfully----------------\n", uid)
}

// Broadcast 广播消息
func (m *WebSocketManager) Broadcast(msg Message) (err error) {
	for _, c := range m.Clients {
		err = c.Write(msg)
	}
	return err
}

// ReceiveMessage 接收消息
func (m *WebSocketManager) ReceiveMessage() {
	msgs, err := m.MessageQueue.Consume()
	if err != nil {
		log.Fatalln("receive message, err:", err)
	}

	for msg := range msgs {
		m.Messages <- msg
	}
}

// HandleMessage 处理消息
func (m *WebSocketManager) HandleMessage() {
	var err error
	var echo Message
	for msg := range m.Messages {
		switch msg.Type {
		case MessageTypeRegister:
			if m.IsManaged(msg.From) {
				echo = NewMessage(MessageTypeRegister, []byte("register success"), "", msg.From)
			} else {
				echo = NewMessage(MessageTypeRegister, []byte("failed register"), "", msg.From)
			}
		case MessageTypeLogout:
			if m.IsManaged(msg.From) {
				echo = NewMessage(MessageTypeEcho, []byte("logged out"), "", msg.From)
			} else {
				echo = NewMessage(MessageTypeEcho, []byte("logged out unmanaged client"), "", msg.From)
			}
		case MessageTypeHeartbeat:
			echo = NewMessage(MessageTypeHeartbeat, []byte("health"), "", msg.From)
		case MessageTypeOneOnOne:
			echo = msg
		case MessageTypeGroup:
			// TODO: 群聊
		case MessageTypeChannel:
		// err = m.Channels[msg.To].Write(msg)
		case MessageTypeBroadcast:
			err = m.Broadcast(msg)
		case MessageTypeEcho:
			msg.To = msg.From
			echo = msg
		default:
			echo = NewMessage(MessageTypeEcho, []byte("format err, can't parse"), "", msg.From)
		}
		err = m.SendMessage(echo)
		if err != nil {
			log.Printf("handle message [%s], err: %s\n", msg.Content, err)
		}
	}
}

func (m *WebSocketManager) PushMessage(msg Message) {
	m.Messages <- msg
}

// IsManaged 判断用户是否被 websocket 管理器 所管理
func (m *WebSocketManager) IsManaged(uid string) bool {
	_, ok := m.Clients[uid]
	return ok
}

// SendMessage 发送消息
func (m *WebSocketManager) SendMessage(msg Message) error {
	if m.IsManaged(msg.To) {
		if err := m.Clients[msg.To].Write(msg); err != nil {
			return err
		}
	}

	wid, err := m.ClientToServerMap.Get(msg.To)
	if err != nil {
		if errors.Is(err, ManagerNotExist) {
			// TODO: 持久化
			fmt.Println("save message")
			return nil
		}
		return err
	}

	m.MessageQueue.Publish(wid, msg)
	return err
}
