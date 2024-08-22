package main

import (
	"container/ring"
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

var sendNeed int32 = 0

// var queueLock sync.Mutex
var maxQueueAge = time.Millisecond * 100
var icmpCh chan *QueueItem

type QueueItem struct {
	ID        int
	Sequence  int
	Timestamp time.Time
}

var sequenceMap sync.Map
var sequenceQueue *ring.Ring
var queueLock sync.Mutex

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

func sendICMP(id int, sequence int, conn net.PacketConn, server *net.IPAddr, target string,
	connId string, msgType uint32, data []byte, sproto int, rproto int, key int,
	tcpmode int, tcpmode_buffer_size int, tcpmode_maxwin int, tcpmode_resend_time int, tcpmode_compress int, tcpmode_stat int,
	timeout int) {

	// 从 channel 中取出标识符和序列号
	atomic.AddInt32(&sendNeed, 1)
	for {
		item := <-icmpCh
		if time.Since(item.Timestamp) > 100*time.Millisecond {
			continue
		}
		id = item.ID
		sequence = item.Sequence
		break
	}

	atomic.AddInt32(&sendNeed, -1)

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
	loggo.Info("Sent ICMP reply  - target IP: %s, connID: %s, Seq: %d, Data: %s\n",
		target, connId, sequence, data)
	loggo.Info("Sent %d bytes to %s", n, server.String())
}

//func listenOnDevice(deviceName string, exit *bool, recv chan<- *Packet) {
//	// 打开网络接口
//	handle, err := pcap.OpenLive(deviceName, 1600, true, pcap.BlockForever)
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer handle.Close()
//
//	// 设置过滤器，只捕获ICMP请求数据包
//	var filter string = "icmp and icmp[icmptype] == icmp-echo"
//	err = handle.SetBPFFilter(filter)
//	if err != nil {
//		log.Fatal(err)
//	}
//	outputChan <- fmt.Sprintf("Listening on device %s for ICMP Echo Requests.", deviceName)
//
//	// 使用gopacket读取数据包
//	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
//	for packet := range packetSource.Packets() {
//		if !*exit {
//			if id, seq, srcIP, data, ok := extractICMPData(packet); ok {
//				loggo.Info("Captured ICMP Request on device %s - Src IP: %s, ID: %d, Seq: %d, Data: %s\n",
//					deviceName, srcIP, id, seq, data)
//
//				if isInQueue(int(id), int(seq)) {
//					loggo.Info("Sequence %d already exists in the queue, continue.", seq)
//					continue // 如果在队列中，则跳过
//				}
//				icmpCh <- &QueueItem{ID: int(id), Sequence: int(seq), Timestamp: time.Now()}
//				enqueue(int(id), int(seq)) // 入队
//				//echoId := id
//				//echoSeq := seq
//
//				//updated := false
//
//				//queueLock.Lock()
//				//for e := icmpQueue.Front(); e != nil; e = e.Next() {
//				//	item := e.Value.(*QueueItem)
//				//	if item.Sequence == int(echoSeq) {
//				//		loggo.Debug("Sequence %d already exists in the queue, updating timestamp. Queue length : %d\n", echoSeq, icmpQueue.Len())
//				//		item.Timestamp = time.Now() // 更新时间戳
//				//		updated = true
//				//		break
//				//	}
//				//}
//				//if !updated {
//				//	icmpQueue.PushBack(&QueueItem{ID: int(echoId), Sequence: int(echoSeq), Timestamp: time.Now()})
//				//}
//				//queueLock.Unlock()
//				//
//				//if updated {
//				//	continue // 如果已更新，则跳过剩余的逻辑
//				//}
//				my := &MyMsg{}
//				err = proto.Unmarshal([]byte(data), my)
//				if err != nil {
//					loggo.Info("Unmarshal MyMsg error: %s", err)
//					continue
//				}
//				if my.Magic != int32(MyMsg_MAGIC) {
//					loggo.Info("processPacket data invalid %s", my.Id)
//					continue
//				}
//
//				srcAddr, err := net.ResolveIPAddr("ip", srcIP)
//				if err != nil {
//					loggo.Info("Failed to resolve IP address: %s", err)
//					continue
//				}
//				recv <- &Packet{
//					my:      my,
//					src:     srcAddr,
//					echoId:  int(id),
//					echoSeq: int(seq),
//				}
//
//			}
//		}
//	}
//	println("exit listen")
//}

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
