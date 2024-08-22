package main

import (
	"flag"
	"fmt"
	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/loggo"
	"github.com/hashicorp/yamux"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

func closeIcmpEcho() {
	if runtime.GOOS == "linux" {
		//关闭icmp响应
		cmdString := "echo 1 > /proc/sys/net/ipv4/icmp_echo_ignore_all"
		cmd := exec.Command("bash", "-c", cmdString)
		output, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Println("Command failed with error: %v", err)
			fmt.Println("Output: %s", string(output))
			return
		}
		fmt.Println("Successfully closed ICMP echo")
		fmt.Println(string(output))
	} else if runtime.GOOS == "windows" {
		//添加防火墙规则
		addCmdString := `netsh advfirewall firewall add rule name="Block ICMP Inbound" protocol=icmpv4:any,any dir=in action=block`
		addcmd := exec.Command("cmd.exe", "/C", addCmdString)
		output, err := addcmd.CombinedOutput()
		if err != nil {
			fmt.Println("Command failed with error: %v\n", err)
			fmt.Println("Output: %s\n", string(output))
			return
		}
		fmt.Println("Successfully added rule: Block ICMP Inbound")
		fmt.Println(string(output))
	}
}

func openIcmpEcho() {
	if runtime.GOOS == "linux" {
		cmdString := "echo 0 > /proc/sys/net/ipv4/icmp_echo_ignore_all"
		icmpOpenCmd := exec.Command("bash", "-c", cmdString)
		output, err := icmpOpenCmd.CombinedOutput()
		if err != nil {
			fmt.Println("Command failed with error: %v", err)
			fmt.Println("Output: %s", string(output))
			return
		}
		fmt.Println("Successfully opened ICMP echo")
		fmt.Println(string(output))

	} else if runtime.GOOS == "windows" {
		delCmdString := `netsh advfirewall firewall delete rule name="Block ICMP Inbound"`
		delCmd := exec.Command("cmd.exe", "/C", delCmdString)
		delOutput, delErr := delCmd.CombinedOutput()
		if delErr != nil {
			fmt.Println("Command failed with error: %v\n", delErr)
			fmt.Println("Output: %s\n", string(delOutput))
			return
		}
		fmt.Println("Successfully deleted rule: Block ICMP Inbound")
		fmt.Println(string(delOutput))
	}
}

func startIcmpServer(stop chan struct{}) {
	defer common.CrashLog()
	key := 123456
	maxconn := 0
	maxProcessThread := 100
	maxProcessBuffer := 1000
	conntt := 1000
	// 初始化日志配置，但不输出日志
	loggo.Ini(loggo.Config{
		Level:     0,
		Prefix:    "pingtunnel",
		MaxDay:    3,
		NoLogFile: true,
		NoPrint:   true,
	})

	// 启动服务器
	s, err := NewServer(key, maxconn, maxProcessThread, maxProcessBuffer, conntt)
	if err != nil {
		fmt.Println("Icmp Server error:", err)
		os.Exit(0)
	}

	log.Println("Opening icmp server...")
	err = s.Run()
	if err != nil {
		fmt.Println("Icmp Server error:", err)
		os.Exit(0)
	}

	// 添加防火墙规则
	closeIcmpEcho()
	<-stop
	s.exit = true
	log.Println("Exiting icmp server..")

}

var httpListenerStarted bool
var httpListenerMutex sync.Mutex

func main() {
	// 定义命令行参数
	lport := flag.String("lport", "8081", "Local listener port")
	hport := flag.String("hport", "8443", "HTTP listener port")
	logOutput := flag.Bool("log", false, "Output logs to console")
	icmpFlag := flag.Bool("icmp", false, "Open icmp server")
	flag.Parse()

	// 设置日志输出
	if !*logOutput {
		log.SetOutput(io.Discard)
	}

	// 启动本地主动连接器的监听端口 A
	listener, err := net.Listen("tcp", ":"+*lport) // 使用命令行参数指定的端口 A
	if err != nil {
		log.Fatalf("Unable to listen on port %s: %v", *lport, err)
	}
	defer listener.Close()

	log.Printf("Listening on port  %s", *lport)

	for {
		// 接受本地主动连接
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Unable to accept connection: %v", err)
			continue
		}

		log.Println("Connected to local service")

		// 创建一个 yamux 会话
		session, err := yamux.Server(conn, nil)
		if err != nil {
			log.Fatalf("Unable to create yamux session: %v", err)
		}

		// 启动虚拟HTTP端口监听（端口 B）
		httpListenerMutex.Lock()
		if !httpListenerStarted {
			httpListenerStarted = true
			httpListenerMutex.Unlock()

			stop := make(chan struct{})
			icmpstop := make(chan struct{})
			var wg sync.WaitGroup

			wg.Add(1)
			go func() {
				defer wg.Done()
				defer close(icmpstop)
				if *icmpFlag {
					go startIcmpServer(icmpstop)
				}
				startHTTPListener(session, stop, *hport)
			}()

			// 监听 yamux 会话的状态，如果会话关闭，则触发停止信号
			go func() {
				for {
					if session.IsClosed() {
						close(stop) // 会话关闭，发送停止信号
						return
					}
					time.Sleep(500 * time.Millisecond)
				}
			}()

			// 监听端口 A 的连接断开情况，并在端口 B 关闭后继续监听端口 A
			go func() {
				wg.Wait() // 等待端口 B 关闭
				conn.Close()
				if *icmpFlag {
					log.Println("Open icmp echo...")
					openIcmpEcho()
				}
				httpListenerMutex.Lock()
				httpListenerStarted = false
				httpListenerMutex.Unlock()
				log.Println("Http listener stopped, ready to accept new connections")
			}()
		} else {
			httpListenerMutex.Unlock()
		}
	}
}

// 启动HTTP监听器
func startHTTPListener(session *yamux.Session, stop chan struct{}, hport string) {
	// 监听TCP端口 B
	httpListener, err := net.Listen("tcp", ":"+hport)
	if err != nil {
		log.Fatalf("Unable to listen on HTTP port %s: %v", hport, err)
		return
	}
	defer httpListener.Close()
	tcpListener := httpListener.(*net.TCPListener)

	log.Printf("HTTP listener started on port %s", hport)

	for {
		// 设置一个相对短的超时时间来避免阻塞
		tcpListener.SetDeadline(time.Now().Add(1 * time.Second)) // 延长超时来避免多次连接的误判

		clientConn, err := tcpListener.Accept()
		if err != nil {
			select {
			case <-stop:
				// 如果停止信号已发出，退出
				log.Println("Stopping HTTP listener")
				return
			default:
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// 超时错误，继续循环
					continue
				}
				log.Printf("Unable to accept client connection: %v", err)
				continue
			}
		}

		log.Println("Client connection established")

		// 在 session 上为每个客户端连接创建一个新的 yamux 流
		stream, err := session.Open()
		if err != nil {
			log.Printf("Unable to open yamux stream: %v", err)
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

	// 将本地服务的数据转发给客户端
	go func() {
		io.Copy(clientConn, stream)
		done <- struct{}{}
	}()

	// 将客户端连接的数据转发给本地服务
	go func() {
		io.Copy(stream, clientConn)
		done <- struct{}{}
	}()

	// 等待本地服务连接关闭
	<-done
	log.Println("Disconnected")
}
