//go:build windows

package main

import (
	"fmt"
	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/loggo"
	"github.com/golang/protobuf/proto"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"log"
	"net"
	"sync"
	"time"
)

func isLoopback(device pcap.Interface) bool {
	for _, address := range device.Addresses {
		if address.IP.IsLoopback() {
			return true
		}
	}
	return false
}

func extractICMPData(packet gopacket.Packet) (uint16, uint16, string, string, bool) {
	ipLayer := packet.Layer(layers.LayerTypeIPv4)
	if ipLayer == nil {
		return 0, 0, "", "", false
	}
	ipPacket, _ := ipLayer.(*layers.IPv4)

	icmpLayer := packet.Layer(layers.LayerTypeICMPv4)
	if icmpLayer == nil {
		return 0, 0, "", "", false
	}
	icmpPacket, _ := icmpLayer.(*layers.ICMPv4)

	// 只处理ICMP请求（Echo Request）
	if icmpPacket.TypeCode.Type() != layers.ICMPv4TypeEchoRequest {
		return 0, 0, "", "", false
	}

	return icmpPacket.Id, icmpPacket.Seq, ipPacket.SrcIP.String(), string(icmpPacket.Payload), true
}

func listenOnDevice(deviceName string, exit *bool, recv chan<- *Packet, conn net.PacketConn) {
	handle, err := pcap.OpenLive(deviceName, 1600, false, pcap.BlockForever) //默认关闭混杂模式
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	var filter string = "icmp and icmp[icmptype] == icmp-echo"
	err = handle.SetBPFFilter(filter)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Listening on device %s for ICMP Echo Requests.", deviceName)

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	for {
		select {
		case packet := <-packetSource.Packets():
			if !*exit {
				if id, seq, srcIP, data, ok := extractICMPData(packet); ok {
					loggo.Info("Captured ICMP Request on device %s - Src IP: %s, ID: %d, Seq: %d, Data: %s\n",
						deviceName, srcIP, id, seq, data)

					my := &MyMsg{}
					err = proto.Unmarshal([]byte(data), my)
					if err != nil {
						if isInQueuePing(int(id), int(seq)) {
							continue
						}
						enqueuePing(int(id), int(seq))
						srcAddr, err := net.ResolveIPAddr("ip4", srcIP) // 直接返回 *net.IPAddr
						if err != nil {
							loggo.Error("src ip convert error")
							continue
						}
						commonReply(int(id), int(seq), []byte(data), conn, srcAddr)
						loggo.Info("Unmarshal MyMsg error: %s", err)
						continue
					}

					if isInQueue(int(id), int(seq)) {
						loggo.Info("Sequence %d already exists in the queue, continue.", seq)
						continue
					}

					// 原子操作：先尝试添加channel，如果成功（返回true）则初始化sendNeed
					if addChannelForID(int(id)) {
						initSendNeed(int(id))
					}

					if err := sendToID(int(id), int(seq)); err != nil {
						loggo.Info("Error sending item:", err)
						continue
					}
					enqueue(int(id), int(seq))

					if my.Magic != int32(MyMsg_MAGIC) {
						loggo.Info("processPacket data invalid %s", my.Id)
						continue
					}

					srcAddr, err := net.ResolveIPAddr("ip", srcIP)
					if err != nil {
						loggo.Info("Failed to resolve IP address: %s", err)
						continue
					}
					recv <- &Packet{
						my:      my,
						src:     srcAddr,
						echoId:  int(id),
						echoSeq: int(seq),
					}

				}
			}
		case <-time.After(time.Second * 10): // 设定一个超时时间来检查 exit 条件
			if *exit {
				return
			}
		}
	}
	println("exit listen")
}

func recvICMP(workResultLock *sync.WaitGroup, exit *bool, conn net.PacketConn, recv chan<- *Packet) {
	defer common.CrashLog()

	workResultLock.Add(1)
	defer workResultLock.Done()
	devices, err := pcap.FindAllDevs()
	if err != nil {
		log.Fatal(err)
	}

	for _, device := range devices {
		if !isLoopback(device) {
			go listenOnDevice(device.Name, exit, recv, conn)
		}
	}

	for !*exit {
		time.Sleep(time.Second * 1) // 不退出
	}

}
