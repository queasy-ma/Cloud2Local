package main

import (
	"flag"
	"io"
	"log"
	"net"
	"time"

	"github.com/hashicorp/yamux"
)

func main() {
	// 定义命令行参数 -s 用于指定连接地址
	address := flag.String("s", "192.168.1.1:8081", "Specify the connection address of the forwarder")
	flag.Parse()

	// 启动客户端
	connectForwarder(*address, "8080")
}

// connectForwarder 封装了连接转发器并转发数据的逻辑
func connectForwarder(address string, hport string) {
	// 连接到公网转发器
	conn, err := net.Dial("tcp", address)
	if err != nil {
		log.Fatalf("Unable to connect to forwarder: %v", err)
	}
	defer conn.Close()

	// 创建一个 yamux 会话
	session, err := yamux.Client(conn, nil)
	if err != nil {
		log.Fatalf("Unable to create yamux session: %v", err)
	}
	defer session.Close()

	for {
		// 等待从公网转发器发送的 yamux 流
		stream, err := session.Accept()
		if err != nil {
			log.Printf("Unable to accept yamux stream: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		// 连接到本地HTTP服务
		localConn, err := net.Dial("tcp", "127.0.0.1:"+hport)
		if err != nil {
			//log.Printf("Unable to connect to local service: %v", err)
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
