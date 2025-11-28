//go:build linux

package main

import (
	"encoding/binary"
	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/loggo"
	"github.com/golang/protobuf/proto"
	"net"
	"sync"
	"time"
)

func recvICMP(workResultLock *sync.WaitGroup, exit *bool, conn net.PacketConn, recv chan<- *Packet) {

	defer common.CrashLog()

	(*workResultLock).Add(1)
	defer (*workResultLock).Done()

	bytes := make([]byte, 10240)
	for !*exit {
		conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, srcaddr, err := conn.ReadFrom(bytes)

		if err != nil {
			nerr, ok := err.(net.Error)
			if !ok || !nerr.Timeout() {
				loggo.Info("Error read icmp message %s", err)
				continue
			}
		}

		if n <= 0 {
			continue
		}
		loggo.Info("Received %d bytes from %s", n, srcaddr)
		//hexData := ""
		//for i := 0; i < n; i++ {
		//	hexData += fmt.Sprintf("%02x ", bytes[i])
		//}
		//loggo.Info("Data (hex): %s", hexData)
		echoId := int(binary.BigEndian.Uint16(bytes[4:6]))
		echoSeq := int(binary.BigEndian.Uint16(bytes[6:8]))

		my := &MyMsg{}
		err = proto.Unmarshal(bytes[8:n], my)
		if err != nil {
			if isInQueuePing(echoId, echoSeq) {
				continue
			}
			enqueuePing(echoId, echoSeq)
			srcAddr, err := net.ResolveIPAddr("ip4", srcaddr.String()) // 直接返回 *net.IPAddr
			if err != nil {
				loggo.Error("src ip convert error")
				continue
			}
			commonReply(echoId, echoSeq, bytes[8:n], conn, srcAddr)
			loggo.Debug("Unmarshal MyMsg error: %s", err)
			continue
		}

		if isInQueue(echoId, echoSeq) {
			loggo.Info("Sequence %d already exists in the queue, continue.", echoSeq)
			continue
		}
		// 将标识符、序列号和当前时间存入队列
		//queueLock.Lock()
		//icmpQueue.PushBack(&QueueItem{ID: echoId, Sequence: echoSeq, Timestamp: time.Now()})
		//queueLock.Unlock()
		// 原子操作：先尝试添加channel，如果成功（返回true）则初始化sendNeed
		if addChannelForID(echoId) {
			initSendNeed(echoId)
		}

		if err := sendToID(echoId, echoSeq); err != nil {
			loggo.Info("Error sending item:", err)
			continue
		}
		enqueue(echoId, echoSeq)

		if my.Magic != (int32)(MyMsg_MAGIC) {
			loggo.Debug("processPacket data invalid %s", my.Id)
			continue
		}
		loggo.Info("Recv packet: Id=%d, Seq=%d, Type=%d, Source IP=%s, Data size=%d", echoId, echoSeq, my.Type, srcaddr.String(), len(my.Data))
		recv <- &Packet{my: my,
			src:    srcaddr.(*net.IPAddr),
			echoId: echoId, echoSeq: echoSeq}
	}
}
