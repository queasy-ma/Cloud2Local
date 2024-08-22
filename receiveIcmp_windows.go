//go:build windows

package main

import (
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

func listenOnDevice(deviceName string, exit *bool, recv chan<- *Packet) {
	handle, err := pcap.OpenLive(deviceName, 1600, true, pcap.BlockForever)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	var filter string = "icmp and icmp[icmptype] == icmp-echo"
	err = handle.SetBPFFilter(filter)
	if err != nil {
		log.Fatal(err)
	}
	//outputChan <- fmt.Sprintf("Listening on device %s for ICMP Echo Requests.", deviceName)

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	for {
		select {
		case packet := <-packetSource.Packets():
			if !*exit {
				if id, seq, srcIP, data, ok := extractICMPData(packet); ok {
					loggo.Info("Captured ICMP Request on device %s - Src IP: %s, ID: %d, Seq: %d, Data: %s\n",
						deviceName, srcIP, id, seq, data)

					if isInQueue(int(id), int(seq)) {
						loggo.Info("Sequence %d already exists in the queue, continue.", seq)
						continue
					}

					icmpCh <- &QueueItem{ID: int(id), Sequence: int(seq), Timestamp: time.Now()}
					enqueue(int(id), int(seq))

					my := &MyMsg{}
					err = proto.Unmarshal([]byte(data), my)
					if err != nil {
						loggo.Info("Unmarshal MyMsg error: %s", err)
						continue
					}
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
			go listenOnDevice(device.Name, exit, recv)
		}
	}

	for !*exit {
		time.Sleep(time.Second * 1) // 不退出
	}

}
