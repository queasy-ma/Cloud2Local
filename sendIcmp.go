package main

import (
	"container/ring"
	"encoding/binary"
	"fmt"
	"github.com/esrrhs/gohome/loggo"
	"github.com/golang/protobuf/proto"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var sendNeedMap sync.Map

// var queueLock sync.Mutex
var maxQueueAge = time.Millisecond * 100
var icmpChMap map[int]chan *QueueItem
var icmpChMapMutex sync.RWMutex // 添加读写锁保护 icmpChMap

type QueueItem struct {
	ID        int
	Sequence  int
	Timestamp time.Time
}

var sequenceMap sync.Map
var sequenceQueue *ring.Ring
var queueLock sync.Mutex

var sequencePingMap sync.Map
var sequencePingQueue *ring.Ring
var queuePingLock sync.Mutex

// 初始化 sendNeed 的值为 0（如果不存在）
func initSendNeed(id int) {
	// 使用原子操作保证在并发情况下不会重复初始化
	if _, loaded := sendNeedMap.LoadOrStore(id, new(int32)); !loaded {
	}
}

// 增加指定 ID 的 sendNeed 值
func incrementSendNeed(id int) {
	if val, ok := sendNeedMap.Load(id); ok {
		atomic.AddInt32(val.(*int32), 1)
	}
}

// 减少指定 ID 的 sendNeed 值
func decrementSendNeed(id int) {
	if val, ok := sendNeedMap.Load(id); ok {
		atomic.AddInt32(val.(*int32), -1)
	}
}

// 获取指定 ID 的 sendNeed 值，并转换为字节数组
func getSendNeedBytes(id int) ([]byte, error) {
	val, ok := sendNeedMap.Load(id)
	if !ok {
		return int32ToBytes(0), nil
	}

	// 获取当前的 int32 值
	n := atomic.LoadInt32(val.(*int32))
	return int32ToBytes(n), nil
}

// int32 转换为字节数组
func int32ToBytes(n int32) []byte {
	bytes := make([]byte, 4)
	binary.BigEndian.PutUint32(bytes, uint32(n))
	return bytes
}

func initQueue(size int) {
	sequenceQueue = ring.New(size)
}

func enqueue(id, seq int) {
	queueLock.Lock()
	defer queueLock.Unlock()

	if sequenceQueue.Value != nil {
		oldItem := sequenceQueue.Value.(QueueItem)
		sequenceMap.Delete(fmt.Sprintf("%d-%d", oldItem.ID, oldItem.Sequence))
	}

	sequenceQueue.Value = QueueItem{ID: id, Sequence: seq, Timestamp: time.Now()}
	sequenceMap.Store(fmt.Sprintf("%d-%d", id, seq), struct{}{})
	sequenceQueue = sequenceQueue.Next()
}

func isInQueue(id, seq int) bool {
	_, ok := sequenceMap.Load(fmt.Sprintf("%d-%d", id, seq))
	return ok
}

func initPingQueue(size int) {
	sequencePingQueue = ring.New(size)
}

func enqueuePing(id, seq int) {
	queuePingLock.Lock()
	defer queuePingLock.Unlock()

	if sequencePingQueue.Value != nil {
		oldItem := sequencePingQueue.Value.(QueueItem)
		sequencePingMap.Delete(fmt.Sprintf("%d-%d", oldItem.ID, oldItem.Sequence))
	}

	sequencePingQueue.Value = QueueItem{ID: id, Sequence: seq, Timestamp: time.Now()}
	sequencePingMap.Store(fmt.Sprintf("%d-%d", id, seq), struct{}{})
	sequencePingQueue = sequencePingQueue.Next()
}

func isInQueuePing(id, seq int) bool {
	_, ok := sequencePingMap.Load(fmt.Sprintf("%d-%d", id, seq))
	return ok
}

func isChannelExists(id int) bool {
	icmpChMapMutex.RLock()
	defer icmpChMapMutex.RUnlock()
	_, exists := icmpChMap[id]
	return exists
}

// 添加新的 ID 和通道到 icmpChMap
// 返回 true 表示成功创建，false 表示已经存在
func addChannelForID(id int) bool {
	icmpChMapMutex.Lock()
	defer icmpChMapMutex.Unlock()

	// 双重检查：在获取锁后再次检查是否存在
	if _, exists := icmpChMap[id]; exists {
		return false // 已存在，不需要创建
	}

	// 创建新的 chan *QueueItem 并将其加入到 icmpChMap 中
	icmpChMap[id] = make(chan *QueueItem, 3000) // 假设通道缓冲区大小为 3000，可根据需要调整
	return true
}

// 发送 QueueItem 到指定 ID 的通道
func sendToID(id int, sequence int) error {
	icmpChMapMutex.RLock()
	ch, ok := icmpChMap[id]
	icmpChMapMutex.RUnlock()

	if !ok {
		return fmt.Errorf("channel for ID %d not found", id)
	}

	// 创建包含 ID 的 QueueItem
	item := &QueueItem{
		ID:        id,
		Sequence:  sequence,
		Timestamp: time.Now(),
	}
	ch <- item
	return nil
}

// 从指定 ID 的通道接收 QueueItem
func receiveFromID(id int) (*QueueItem, error) {
	icmpChMapMutex.RLock()
	ch, ok := icmpChMap[id]
	icmpChMapMutex.RUnlock()

	if !ok {
		return nil, fmt.Errorf("channel for ID %d not found", id)
	}
	item := <-ch
	return item, nil
}

func commonReply(id int, sequence int, data []byte, conn net.PacketConn, srcAddr *net.IPAddr) {
	body := &icmp.Echo{
		ID:   id,
		Seq:  sequence,
		Data: data,
	}

	msg := &icmp.Message{
		Type: (ipv4.ICMPType)(0),
		Code: 0,
		Body: body,
	}

	bytes, err := msg.Marshal(nil)
	if err != nil {
		loggo.Error("sendICMP Marshal error %s %s", srcAddr.String(), err)
		return
	}

	n, err := conn.WriteTo(bytes, srcAddr)
	if err != nil {
		loggo.Error("sendICMP WriteTo error %s %s", srcAddr.String(), err)
		return
	}
	//sendIcmpCount()
	loggo.Info("Sent ICMP reply  -  Seq: %d ===== Data: %s ===== Sent %d bytes to %s\n",
		sequence, data, n, srcAddr.String())
}

func sendICMP(id int, sequence int, conn net.PacketConn, server *net.IPAddr, target string,
	connId string, msgType uint32, data []byte, sproto int, rproto int, key int,
	tcpmode int, tcpmode_buffer_size int, tcpmode_maxwin int, tcpmode_resend_time int, tcpmode_compress int, tcpmode_stat int,
	timeout int) {

	// 从 channel 中取出标识符和序列号
	incrementSendNeed(id)
	for {
		item, err := receiveFromID(id)
		if err != nil {
			loggo.Info("Error receiving item")
			return
		}

		if time.Since(item.Timestamp) >
			100*time.Millisecond {
			continue
		}
		id = item.ID
		sequence = item.Sequence
		break
	}

	decrementSendNeed(id)

	m := &MyMsg{
		Id:                  connId,
		Type:                (int32)(msgType),
		Target:              target,
		Data:                data,
		Rproto:              (int32)(rproto),
		Key:                 (int32)(key),
		Tcpmode:             (int32)(tcpmode),
		TcpmodeBuffersize:   (int32)(tcpmode_buffer_size),
		TcpmodeMaxwin:       (int32)(tcpmode_maxwin),
		TcpmodeResendTimems: (int32)(tcpmode_resend_time),
		TcpmodeCompress:     (int32)(tcpmode_compress),
		TcpmodeStat:         (int32)(tcpmode_stat),
		Timeout:             (int32)(timeout),
		Magic:               (int32)(MyMsg_MAGIC),
	}

	mb, err := proto.Marshal(m)
	if err != nil {
		loggo.Error("sendICMP Marshal MyMsg error %s %s", server.String(), err)
		return
	}

	// 输出序列化后的数据
	//hexData := ""
	//for _, b := range mb {
	//	hexData += fmt.Sprintf("%02x ", b)
	//}
	//loggo.Info("Serialized MyMsg: %s", hexData)

	body := &icmp.Echo{
		ID:   id,
		Seq:  sequence,
		Data: mb,
	}

	msg := &icmp.Message{
		Type: (ipv4.ICMPType)(0),
		Code: 0,
		Body: body,
	}

	bytes, err := msg.Marshal(nil)
	if err != nil {
		loggo.Error("sendICMP Marshal error %s %s", server.String(), err)
		return
	}
	// 输出整个ICMP消息的字节数据
	//hexMsg := ""
	//for _, b := range bytes {
	//	hexMsg += fmt.Sprintf("%02x ", b)
	//}
	//loggo.Info("ICMP Message: %s", hexMsg)
	n, err := conn.WriteTo(bytes, server)
	if err != nil {
		loggo.Error("sendICMP WriteTo error %s %s", server.String(), err)
		return
	}
	//sendIcmpCount()
	loggo.Info("Sent ICMP reply  - target IP: %s, connID: %s, Seq: %d, Data size: %d\n",
		target, connId, sequence, len(data))
	loggo.Info("Sent %d bytes to %s", n, server.String())
}

type Packet struct {
	my      *MyMsg
	src     *net.IPAddr
	echoId  int
	echoSeq int
}

const (
	FRAME_MAX_SIZE int = 888
	FRAME_MAX_ID   int = 1000000
)
