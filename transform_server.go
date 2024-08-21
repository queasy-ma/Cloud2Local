package main

import (
	"io"
	"log"
	"net"
	"sync"

	"github.com/hashicorp/yamux"
)

func main() {
	// 启动本地主动连接器的监听端口
	listener, err := net.Listen("tcp", ":8081") // 本地主动连接器连接端口
	if err != nil {
		log.Fatalf("无法监听端口: %v", err)
	}
	defer listener.Close()

	var session *yamux.Session
	//var httpListener net.Listener
	var muxSessionSetup sync.Once

	for {
		// 接受本地主动连接
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("无法接受连接: %v", err)
			continue
		}

		log.Println("已连接到本地服务")

		// 创建一个 yamux 会话
		session, err = yamux.Server(conn, nil)
		if err != nil {
			log.Fatalf("无法创建 yamux 会话: %v", err)
		}

		// 启动虚拟HTTP端口监听，并确保它一直运行
		muxSessionSetup.Do(func() {
			go startHTTPListener(session)
		})
	}
}

// 启动HTTP监听器
func startHTTPListener(session *yamux.Session) {
	httpListener, err := net.Listen("tcp", ":8443") // 虚拟HTTP端口
	if err != nil {
		log.Fatalf("无法监听HTTP端口: %v", err)
	}
	defer httpListener.Close()

	for {
		clientConn, err := httpListener.Accept()
		if err != nil {
			log.Printf("无法接受客户端连接: %v", err)
			continue
		}

		log.Println("客户端连接已建立")

		// 在 session 上为每个客户端连接创建一个新的 yamux 流
		stream, err := session.Open()
		if err != nil {
			log.Printf("无法打开 yamux 流: %v", err)
			clientConn.Close()
			continue
		}

		// 将客户端连接与创建的流进行转发
		go handleClientConnection(clientConn, stream)
	}
}

// 处理客户端连接的转发
func handleClientConnection(clientConn net.Conn, stream net.Conn) {
	defer clientConn.Close()
	defer stream.Close()

	done := make(chan struct{})

	// 将客户端连接的数据转发给本地服务
	go func() {
		io.Copy(stream, clientConn)
		done <- struct{}{}
	}()

	// 将本地服务的数据转发给客户端
	go func() {
		io.Copy(clientConn, stream)
		done <- struct{}{}
	}()

	// 等待任意一方连接关闭
	<-done
	log.Println("连接已断开")
}
