package main

import (
	"context"
	"encoding/json"
	"github.com/alexflint/go-arg"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	ifces "github.com/shirou/gopsutil/net"
	"github.com/sirupsen/logrus"
	"net/http"
	"os"
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
	logger.Infof("server at:%s", args.Address)
	err := http.ListenAndServe(args.Address, nil) //设置监听的端口
	if err != nil {
		logger.Fatal("ListenAndServe: ", err)
	}
}
