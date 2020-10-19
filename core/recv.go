package core

import (
	"bufio"
	"fmt"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"ksubdomain/gologger"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"net/http"	
)

func Recv(device string, options *Options, flagID uint16, retryChan chan RetryStruct) {
	var (
		snapshotLen int32         = 1024
		promiscuous bool          = false
		timeout     time.Duration = -1 * time.Second
	)
	windowWith := GetWindowWith()
	if options.Silent {
		windowWith = 0
	}
	handle, _ := pcap.OpenLive(device, snapshotLen, promiscuous, timeout)
	err := handle.SetBPFFilter("udp and src port 53")
	if err != nil {
		gologger.Fatalf("SetBPFFilter Faild:%s\n", err.Error())
	}
	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	defer handle.Close()

	var udp layers.UDP
	var dns layers.DNS
	var eth layers.Ethernet
	var ipv4 layers.IPv4
	var ipv6 layers.IPv6

	parser := gopacket.NewDecodingLayerParser(
		layers.LayerTypeEthernet, &eth, &ipv4, &ipv6, &udp, &dns)
	var isWrite bool = false
	var isttl bool = options.TTL
	var isSummary bool = options.Summary
	if options.Output != "" {
		isWrite = true
	}
	var foutput *os.File
	if isWrite {
		foutput, err = os.OpenFile(options.Output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0664)
		if err != nil {
			gologger.Errorf("写入结果文件失败：%s\n", err.Error())
		}
	}
	for {
		packet, err := packetSource.NextPacket()
		if err != nil {
			continue
		}
		var decoded []gopacket.LayerType
		err = parser.DecodeLayers(packet.Data(), &decoded)
		if err != nil {
			continue
		}
		if !dns.QR {
			continue
		}
		if dns.ID/100 == flagID {
			atomic.AddUint64(&RecvIndex, 1)
			udp, _ := packet.Layer(layers.LayerTypeUDP).(*layers.UDP)
			index := GenerateMapIndex(dns.ID%100, uint16(udp.DstPort))
			data, err := LocalStauts.SearchFromIndexAndDelete(uint32(index))
			if err == nil {
				// 处理多级域名
				if dns.ANCount > 0 && data.v.DomainLevel < options.DomainLevel {
					running := true
					if options.SkipWildCard {
						if IsWildCard(data.v.Domain) {
							running = false
						}
					}
					if running {
						for _, sub := range GetSubNextData() {
							subdomain := sub + "." + data.v.Domain
							retryChan <- RetryStruct{Domain: subdomain, Dns: data.v.Dns, SrcPort: 0, FlagId: 0, DomainLevel: data.v.DomainLevel + 1}
						}
					}
				}
				if LocalStack.Len() <= 50000 {
					LocalStack.Push(uint32(index))
				}
			}
			if dns.ANCount > 0 {
				atomic.AddUint64(&SuccessIndex, 1)
				if len(dns.Questions) == 0 {
					continue
				}
				data := RecvResult{Subdomain: string(dns.Questions[0].Name)}
				data.Answers = dns.Answers

				msg := data.Subdomain + " => "
				if !options.Silent {
					for _, v := range data.Answers {
						msg += v.String()
						if isttl {
							msg += " ttl:" + strconv.Itoa(int(v.TTL))
						}
						msg += " => "
					}
				}
				msg = strings.Trim(msg, " => ")
				ff := windowWith - len(msg) - 1
				if !options.Silent {
					if windowWith > 0 && ff > 0 {
						gologger.Silentf("\r%s% *s\n", msg, ff, "")
					} else {
						gologger.Silentf("\r%s\n", msg)
					}
				} else {
					gologger.Silentf("%s\n", msg)
				}
				url := "https://biu.seeksec.cn/api/domain"
				method := "POST"
				payload := strings.NewReader("{\"domain\":\"qq.com\",\"monitor\":false,\"hosts\":\""+data.Subdomain+"\"}")
			  
				client := &http.Client {
				}
				req, err := http.NewRequest(method, url, payload)
				req.Header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.16; rv:81.0) Gecko/20100101 Firefox/81.0")
				req.Header.Add("Content-Type", "application/json;charset=utf-8")
				req.Header.Add("Biu-Api-Key", options.BiuKey)
			  
				res, err := client.Do(req)
				defer res.Body.Close()
				if err != nil {
				  fmt.Println(err)
				}
				if isSummary {
					AsnResults = append(AsnResults, data)
				}
				if isWrite {
					w := bufio.NewWriter(foutput)
					_, err = w.WriteString(msg + "\n")
					if err != nil {
						gologger.Errorf("写入结果文件失败.\n", err.Error())
					}
					w.Flush()
				}
			}
			PrintStatus()
		}
	}
}
