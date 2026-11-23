//go:build !windows

// 개발/테스트용: Windows가 아닌 환경에서는 창 없이 서버만 실행합니다.
package main

import (
	"log"
	"os"
	"os/signal"
)

func main() {
	a := NewApp(LoadSettings())
	if err := a.Start(); err != nil {
		log.Fatal(err)
	}
	log.Printf("LP Now Playing 서버 실행 중: http://127.0.0.1:%d/overlay?target=preview", a.Settings().Port)
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	a.Stop()
}
