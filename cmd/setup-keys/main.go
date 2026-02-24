package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	polymarket "github.com/GoPolymarket/polymarket-go-sdk"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/auth"
)

func main() {
	pk := strings.TrimSpace(os.Getenv("POLYMARKET_PK"))
	if pk == "" {
		log.Fatal("请设置 POLYMARKET_PK 环境变量（你的钱包私钥）")
	}

	signer, err := auth.NewPrivateKeySigner(pk, 137)
	if err != nil {
		log.Fatalf("私钥无效: %v", err)
	}

	sdkClient := polymarket.NewClient()
	clobClient := sdkClient.CLOB.WithAuth(signer, nil)

	resp, err := clobClient.CreateOrDeriveAPIKey(context.Background())
	if err != nil {
		log.Fatalf("创建 API Key 失败: %v", err)
	}

	fmt.Println("=== API 凭证已生成 ===")
	fmt.Println()
	fmt.Printf("export POLYMARKET_API_KEY=\"%s\"\n", resp.APIKey)
	fmt.Printf("export POLYMARKET_API_SECRET=\"%s\"\n", resp.Secret)
	fmt.Printf("export POLYMARKET_API_PASSPHRASE=\"%s\"\n", resp.Passphrase)
	fmt.Println()
	fmt.Println("把上面三行和你的私钥一起写入 ~/.zshrc 或直接粘贴到终端。")
	fmt.Println("然后运行: cd", os.Getenv("PWD"), "&& ./trader")
}
