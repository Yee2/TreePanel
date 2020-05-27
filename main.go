package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/alexflint/go-arg"
	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	ifces "github.com/shirou/gopsutil/net"
	"github.com/sirupsen/logrus"
	"gopkg.in/ini.v1"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

var (
	logger = &logrus.Logger{
		Out:       os.Stdout,
		Formatter: &logrus.TextFormatter{ForceColors: true},
		Level:     logrus.TraceLevel,
	}

	args struct {
		Verbose bool `arg:"-v"`
		Address string
		Dir     string
	}
)

func main() {
	args.Address = "localhost:8666"
	args.Dir = "html"
	arg.MustParse(&args)
	http.Handle("/", http.FileServer(http.Dir(args.Dir)))
	http.HandleFunc("/memory.json", func(writer http.ResponseWriter, _ *http.Request) {
		encoder := json.NewEncoder(writer)
		info, _ := mem.VirtualMemory()
		encoder.Encode(info)
	})
	http.HandleFunc("/disk.json", func(writer http.ResponseWriter, _ *http.Request) {
		encoder := json.NewEncoder(writer)
		list, err := disk.Partitions(false)
		if err != nil {
			encoder.Encode([]interface{}{})
			return
		}
		result := make([]interface{}, 0)
		for _, stat := range list {
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second*3))
			info, err := disk.UsageWithContext(ctx, stat.Mountpoint)
			if err == nil {
				result = append(result, info)
				cancel()
			}
		}
		encoder.Encode(result)
	})
	http.HandleFunc("/interface.json", func(writer http.ResponseWriter, _ *http.Request) {
		encoder := json.NewEncoder(writer)
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second*3))
		ifList, err := ifces.InterfacesWithContext(ctx)
		if err != nil {
			encoder.Encode([]interface{}{})
			return
		}
		cancel()
		encoder.Encode(ifList)
	})
	http.HandleFunc("/cpu.json", func(writer http.ResponseWriter, _ *http.Request) {
		encoder := json.NewEncoder(writer)
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second*3))
		info, err := cpu.InfoWithContext(ctx)
		if err != nil {
			encoder.Encode([]interface{}{})
			return
		}
		cancel()
		encoder.Encode(info)
	})
	http.HandleFunc("/reboot.php", reboot)
	http.HandleFunc("/frpc.json", frpc)
	http.HandleFunc("/network.json", network)
	logger.Infof("server at:%s", args.Address)
	err := http.ListenAndServe(args.Address, nil) //设置监听的端口
	if err != nil {
		logger.Fatal("ListenAndServe: ", err)
	}
}

func reboot(writer http.ResponseWriter, r *http.Request) {
	http.Redirect(writer, r, "/reboot.html", 301)
	go func() {
		time.Sleep(time.Second * 3)
		cmd := exec.Command("systemctl", "reboot")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err != nil {
			logger.Warnf("重啓失敗:%s", err)
		}
	}()
}

var frpConfig struct {
	Common struct {
		Address string `ini:"server_addr" json:"address"`
		Token   string `ini:"token" json:"token"`
		Port    int    `ini:"server_port" json:"port"`
	} `ini:"common" json:"common"`
	Web struct {
		Type    string `ini:"type" json:"type"`
		Port    int    `ini:"local_port" json:"port"`
		Domains string `ini:"custom_domains" json:"domains"`
	} `ini:"-" json:"web"`
}

const localPort = 80 // 本地服务端口锁死
func frpc(writer http.ResponseWriter, r *http.Request) {

	if r.Method == "POST" {
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&frpConfig); err != nil {
			writer.Write([]byte("解码失败:" + err.Error()))
			return
		}
		frpConfig.Web.Type = "http"
		frpConfig.Web.Port = localPort
		cfg := ini.Empty()
		if err := cfg.ReflectFrom(&frpConfig); err != nil {
			writer.Write([]byte("系统错误:" + err.Error()))
			return
		}
		cfg.Section(fmt.Sprintf("web_%d", time.Now().Unix())).ReflectFrom(&frpConfig.Web)
		if err := cfg.SaveTo("/etc/frp/frpc.ini"); err != nil {
			writer.Write([]byte("保存失败:" + err.Error()))
			return
		}
		writer.Write([]byte("保存完成，正在重启 frp 服务"))
		go func() {
			if err := restart("frpc"); err != nil {
				logger.Warnf("重启内网穿透服务失败:%s", err)
			}
		}()
		return

	}
	cfg, err := ini.Load("/etc/frp/frpc.ini")
	if err != nil {
		logger.Warnf("读取frp配置文件失败:%s", err)
	} else {
		cfg.MapTo(&frpConfig)
	}
	for _, section := range cfg.Sections() {
		key, err := section.GetKey("type")
		if err == nil && key.String() == "http" {
			section.MapTo(&frpConfig.Web)
		}
	}
	encoder := json.NewEncoder(writer)
	encoder.Encode(frpConfig)
}

func restart(service string) error {
	if !strings.HasSuffix(service, ".service") {
		service = service + ".service"
	}
	sysD, err := dbus.NewSystemdConnection()
	if err != nil {
		return fmt.Errorf("连接到 systemd 出现错误:%w", err)
	}
	defer sysD.Close()
	if _, err := sysD.StartUnit(service, "replace", nil); err != nil {
		return err
	}
	return nil
}

type NetworkConfig struct {
	Unset bool `ini:"-"`
	Match struct {
		Name string
	}

	Network struct {
		DHCP    string
		Address string
		Gateway string
		DNS     string
	}
}

func network(writer http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		setNetwork(writer, r)
		return
	}
	encoder := json.NewEncoder(writer)
	ifList, err := net.Interfaces()
	if err != nil {
		encoder.Encode([]struct{}{})
		return
	}
	result := make([]*NetworkConfig, 0)
	for _, i := range ifList {
		if i.Name == "lo" {
			continue
		}
		if strings.HasPrefix(i.Name, "docker") || strings.HasPrefix(i.Name, "veth") {
			// 过滤掉 docker 的虚拟网卡j
			continue
		}
		nCfg := &NetworkConfig{}
		cfg, err := ini.Load("/etc/systemd/network/10_" + i.Name + ".network")
		if err == nil && cfg.MapTo(nCfg) == nil {
		} else {
			nCfg.Unset = true
			nCfg.Match.Name = i.Name
		}
		result = append(result, nCfg)
	}
	encoder.Encode(result)
}
func setNetwork(writer http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	cfg := &NetworkConfig{}
	if err := decoder.Decode(cfg); err != nil {
		writer.Write([]byte("非法请求"))
		return
	}
	ifList, err := net.Interfaces()
	if err != nil {
		writer.Write([]byte("系统错误"))
		return
	}
	valid := false
	for _, i := range ifList {
		if i.Name == "lo" {
			continue
		}
		if strings.HasPrefix(i.Name, "docker") || strings.HasPrefix(i.Name, "veth") {
			// 过滤掉 docker 的虚拟网卡j
			continue
		}
		if i.Name == cfg.Match.Name {
			valid = true
			break
		}

	}
	if !valid {
		writer.Write([]byte("网卡未找到"))
		return
	}
	if cfg.Unset {
		os.Remove("/etc/systemd/network/10_" + cfg.Match.Name + ".network")
		writer.Write([]byte("已清除网卡配置，正在重启网络"))
		go func() {
			if err := restart("systemd-networkd"); err != nil {
				logger.Warnf("重启网络失败:%s", err)
			}
		}()
		return
	} else if _, _, err := net.ParseCIDR(cfg.Network.Address); !(cfg.Network.DHCP == "yes" && cfg.Network.Address == "") && err != nil {
		// DHCP开启的时候，网关和IP地址可以为空
		writer.Write([]byte("IP地址格式错误"))
		return
	} else if !(cfg.Network.DHCP == "yes" && cfg.Network.Gateway == "") && net.ParseIP(cfg.Network.Gateway) == nil {
		writer.Write([]byte("网关地址格式错误"))
		return
	} else if cfg.Network.DNS != "" && net.ParseIP(cfg.Network.DNS) == nil {
		writer.Write([]byte("DNS地址格式错误"))
		return
	}

	iniFile := ini.Empty()
	if err := iniFile.ReflectFrom(&cfg); err != nil {
		writer.Write([]byte("系统错误:" + err.Error()))
		return
	}
	if err := iniFile.SaveTo("/etc/systemd/network/10_" + cfg.Match.Name + ".network"); err != nil {
		writer.Write([]byte("保存失败:" + err.Error()))
		return
	}
	writer.Write([]byte("保存完成,正在重启网络"))
	go func() {
		if err := restart("systemd-networkd"); err != nil {
			logger.Warnf("重启网络失败:%s", err)
		}
	}()
}
