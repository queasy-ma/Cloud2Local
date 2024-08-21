package main

import (
	"io"
	"log"
	"net"
	"time"

	"github.com/hashicorp/yamux"
)

func main() {
	// 连接到公网转发器
	conn, err := net.Dial("tcp", "10.5.1.115:8081")
	if err != nil {
		log.Fatalf("无法连接到转发器: %v", err)
	}
	defer conn.Close()

	// 创建一个 yamux 会话
	session, err := yamux.Client(conn, nil)
	if err != nil {
		log.Fatalf("无法创建 yamux 会话: %v", err)
	}
	defer session.Close()

	for {
		// 等待从公网转发器发送的 yamux 流
		stream, err := session.Accept()
		if err != nil {
			log.Printf("无法接受 yamux 流: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		// 连接到本地HTTP服务
		localConn, err := net.Dial("tcp", "127.0.0.1:8080")
		if err != nil {
			log.Printf("无法连接到本地服务: %v", err)
			stream.Close()
			time.Sleep(1 * time.Second)
			continue
		}

		// 将数据从公网转发器转发给本地服务
		go func() {
			defer localConn.Close()
			defer stream.Close()
			io.Copy(localConn, stream)
		}()

		// 将数据从本地服务转发给公网转发器
		go func() {
			defer localConn.Close()
			defer stream.Close()
			io.Copy(stream, localConn)
		}()
	}
}
