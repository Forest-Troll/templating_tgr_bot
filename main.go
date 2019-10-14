package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"github.com/flosch/pongo2"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gopkg.in/telegram-bot-api.v4"
	"gopkg.in/yaml.v2"

)


type Config struct {
	TelegramToken     string `yaml:"telegram_token"`
	TemplatePath      string `yaml:"template_path"`
	TimeZone          string `yaml:"time_zone"`
	TimeOutFormat     string `yaml:"time_outdata"`
	SplitChart        string `yaml:"split_token"`
	SplitMessageBytes int    `yaml:"split_msg_byte"`
}


// Global
var config_path = flag.String("c", "config.yaml", "Path to a config file")
var listen_addr = flag.String("l", ":9087", "Listen address")
var template_path = flag.String("t", "", "Path to a template file")
var debug = flag.Bool("d", false, "Debug template")

var cfg = Config{}
var bot *tgbotapi.BotAPI
var tmpH map[string]*pongo2.Template



func telegramBot(bot *tgbotapi.BotAPI) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates, err := bot.GetUpdatesChan(u)
	if err != nil {
		log.Fatal(err)
	}

	introduce := func(update tgbotapi.Update) {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("Chat id is '%d'", update.Message.Chat.ID))
		bot.Send(msg)
	}

	for update := range updates {
		if update.Message == nil {
			if *debug {
				log.Printf("[UNKNOWN_MESSAGE] [%v]", update)
			}
			continue
		}

		if update.Message.NewChatMembers != nil && len(*update.Message.NewChatMembers) > 0 {
			for _, member := range *update.Message.NewChatMembers {
				if member.UserName == bot.Self.UserName && update.Message.Chat.Type == "group" {
					introduce(update)
				}
			}
		} else if update.Message != nil && update.Message.Text != "" {
			introduce(update)
		}
	}
}

func loadTemplate(tmplPath string) (*pongo2.Template, error) {
	// let's read template
	return pongo2.FromFile(tmplPath)
}

func SplitString(s string, n int) []string {
	sub := ""
	subs := []string{}

	runes := bytes.Runes([]byte(s))
	l := len(runes)
	for i, r := range runes {
		sub = sub + string(r)
		if (i+1)%n == 0 {
			subs = append(subs, sub)
			sub = ""
		} else if (i + 1) == l {
			subs = append(subs, sub)
		}
	}

	return subs
}

func main() {
	flag.Parse()

	content, err := ioutil.ReadFile(*config_path)
	if err != nil {
		log.Fatalf("Problem reading configuration file: %v", err)
	}
	err = yaml.Unmarshal(content, &cfg)
	if err != nil {
		log.Fatalf("Error parsing configuration file: %v", err)
	}

	if *template_path != "" {
		cfg.TemplatePath = *template_path
	}

	if cfg.SplitMessageBytes == 0 {
		cfg.SplitMessageBytes = 4000
	}

	bot_tmp, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		log.Fatal(err)
	}

	bot = bot_tmp
	if *debug {
		bot.Debug = true
	}
	if cfg.TemplatePath != "" {
		
		tmpH = make(map[string]*pongo2.Template)
		log.Printf("Default template: %s", cfg.TemplatePath)
		tmpH["default"],err = loadTemplate(cfg.TemplatePath)
		if err != nil {
			log.Fatalf("%s", err)
		}

		if cfg.TimeZone == "" {
			log.Fatalf("You must define time_zone of your bot")
			panic(-1)
		}

	} else {
		log.Fatalf("You must define template path")
		panic(-1)
	}
	if !(*debug) {
		gin.SetMode(gin.ReleaseMode)
	}

	log.Printf("Authorised on account %s", bot.Self.UserName)

	go telegramBot(bot)

	router := gin.Default()

	router.GET("/ping/:chatid", GET_Handling)
	router.POST("/alert/:chatid", POST_Handling)
	router.Run(*listen_addr)
}

func GET_Handling(c *gin.Context) {
	log.Printf("Received GET")
	chatid, err := strconv.ParseInt(c.Param("chatid"), 10, 64)
	if err != nil {
		log.Printf("Cat't parse chat id: %q", c.Param("chatid"))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"err": fmt.Sprint(err),
		})
		return
	}

	log.Printf("Bot test: %d", chatid)
	msgtext := fmt.Sprintf("Some HTTP triggered notification by prometheus bot... %d", chatid)
	msg := tgbotapi.NewMessage(chatid, msgtext)
	sendmsg, err := bot.Send(msg)
	if err == nil {
		c.String(http.StatusOK, msgtext)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{
			"err":     fmt.Sprint(err),
			"message": sendmsg,
		})
	}
}

func AlertFormatTemplate(alerts map[string]interface{}, template string) string {
	var bytesBuff bytes.Buffer
	var err error
		
	if template == "" {
		template = "default"
	}
	
	writer := io.Writer(&bytesBuff)

	_, ok := tmpH[template]
	
	if !ok || *debug {
		log.Printf("Reloading Template %s\n", template)
		if template == "default" {
			tmpH[template],err = loadTemplate(cfg.TemplatePath)
		} else {
			tmpH[template],err = loadTemplate(template)
		}
		if err != nil {
			log.Printf("Problem with load template: %s %s", template, err)
			tmpH[template] = tmpH["default"]
		} 
	}

	err = tmpH[template].ExecuteWriterUnbuffered(pongo2.Context(alerts), writer)
	log.Println(alerts)

	if err != nil {
		log.Fatalf("Problem with template execution: %v", err)
		panic(err)
	} 

	return bytesBuff.String()
}

func POST_Handling(c *gin.Context) {
	var msgtext string

	template := c.Query("template")

	chatid, err := strconv.ParseInt(c.Param("chatid"), 10, 64)

	log.Printf("Bot alert post: %d", chatid)

	if err != nil {
		log.Printf("Cat't parse chat id: %q", c.Param("chatid"))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"err": fmt.Sprint(err),
		})
		return
	}
	
	s := make(map[string]interface{})
	binding.JSON.Bind(c.Request, &s)
	
	if *debug {
		ln, err := json.Marshal(s)
		
		if err != nil {
			log.Print(err)
			return
		}


		log.Println("+------------------  A L E R T  J S O N  -------------------+")
		log.Printf("%s", ln)
		log.Println("+-----------------------------------------------------------+\n\n")
	}
	// Decide how format Text
	msgtext = AlertFormatTemplate(s, template)
	
	for _, subString := range SplitString(msgtext, cfg.SplitMessageBytes) {
		msg := tgbotapi.NewMessage(chatid, subString)
		msg.ParseMode = tgbotapi.ModeHTML

		if *debug {
			// Print in Log result message
			log.Println("+---------------  F I N A L   M E S S A G E  ---------------+")
			log.Println(subString)
			log.Println("+-----------------------------------------------------------+")
		}
		
		msg.DisableWebPagePreview = true

		sendmsg, err := bot.Send(msg)
		if err == nil {
			c.String(http.StatusOK, "telegram msg sent.")
		} else {
			log.Printf("Error sending message: %s", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"err":     fmt.Sprint(err),
				"message": sendmsg,
				"srcmsg":  fmt.Sprint(msgtext),
			})
			msg := tgbotapi.NewMessage(chatid, "Error sending message, checkout logs")
			bot.Send(msg)
		}
	}

}
