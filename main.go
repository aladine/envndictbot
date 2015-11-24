package main

import (
	"encoding/json"
	"errors"
	"fmt"
	log "github.com/Sirupsen/logrus"
	util "github.com/aladine/dictutil"
	"github.com/tucnak/telebot"
	"gopkg.in/redis.v3"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	LINK         = "https://telegram.me/storebot?start=envndictbot"
	GREETING_MSG = "Chào %s, xin bắt đầu bằng cách gõ bất kỳ từ tiếng Anh muốn tra. \n" +
		"Hi %s, please type any English word to look up. \n\n" +
		"Rate here: 🌟🌟🌟🌟🌟\n" + LINK + "\n\n"
	HELP_MSG                 = "Xin vui lòng gõ từ bạn muốn tra.\n\nPlease type any word to look up."
	APOLOGY_MSG              = "Xin lỗi %s, em không biết nghĩa của từ \"%s\". \n\nPlease accept my apology, %s. I don't know any definition of \"%s\""
	BYE_MSG                  = "Bye!"
	BOT_NAME                 = "@envndictbot"
	BOT_TOKEN                = "YOUR_TOKEN_FROM_BOTFATHER"
	YANDEX_TRANSLATOR_APIKEY = ""
	YURL                     = "https://translate.yandex.net/api/v1.5/tr.json/translate"
)

var redis_client *redis.Client
var r, _ = regexp.Compile(`^[[:blank:][:graph:]]+$`)
var _, filename, _, _ = runtime.Caller(0)
var base = path.Join(path.Dir(filename), "./data/en_vi")
var dict = util.NewDictionary(base)

type Result struct {
	Code int      `json:"code"`
	Lang string   `json:"lang"`
	Text []string `json:"text"`
}

func main() {

	//init redis
	redis_client = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1,
	})

	dict.PrepareIndex()

	bot, err := telebot.NewBot(BOT_TOKEN)
	if err != nil {
		return
	}

	util.LogInit()

	messages := make(chan telebot.Message)
	bot.Listen(messages, 2*time.Second)

	for message := range messages {
		if message.IsService() {
			continue
		}
		sender_id := message.Chat.ID

		// analytics purpose
		k := "u:" + strconv.Itoa(sender_id)
		redis_client.ZIncrBy("sorted_users", 1, k)

		definition := GetDefinition(message.Text, message.Chat)

		err = bot.SendMessage(message.Chat, definition, &telebot.SendOptions{ParseMode: "Markdown"})
		if err != nil {
			util.LogError("sendMessage", sender_id, err)
		}
	}
}

func GetDefinition(msg string, sender telebot.Chat) string {
	var definition string
	if sender.IsGroupChat() {
		if strings.HasPrefix(msg, BOT_NAME) {
			msg = msg[len(BOT_NAME):]
		} else if strings.HasSuffix(msg, BOT_NAME) {
			msg = msg[:len(msg)-len(BOT_NAME)]
		} else {
			return ""
		}
	}

	if strings.HasPrefix(msg, "/start") {
		definition = fmt.Sprintf(GREETING_MSG, GetName(sender), GetName(sender))
	} else if strings.HasPrefix(msg, "/") || msg == "" {
		definition = HELP_MSG
	} else if strings.HasPrefix(msg, "/stop") {
		definition = BYE_MSG
	} else {
		definition = GetDefinitionFromDb(msg, sender)
	}
	return definition
}

func GetDefinitionFromDb(word string, sender telebot.Chat) string {
	word = strings.Trim(strings.ToLower(word), " ")
	sender_id := sender.ID

	// analytics purpose
	w := "w:" + word
	redis_client.ZIncrBy("sorted_words", 1, w)

	// should get from redis
	desc, err := redis_client.Get(word).Result()
	if err == redis.Nil {
		result, err := dict.Check(word)
		if err != nil {
			if r.MatchString(word) {
				def, err := GetDefinitionYandex(word)
				if err != nil {
					util.LogError("yandex", sender_id, err.Error())
					return fmt.Sprintf(APOLOGY_MSG, GetName(sender), word, GetName(sender), word)
				} else {
					SetWord(word, def)
					return def
				}
			} else {
				return fmt.Sprintf(APOLOGY_MSG, GetName(sender), word, GetName(sender), word)
			}

		} else {
			SetWord(word, result)
			return result
		}
	} else if err != nil {
		log.Errorln(err.Error())
		return ""
	} else {
		return desc
	}
}

func GetDefinitionYandex(word string) (string, error) {
	params := url.Values{}

	params.Set("format", "plain")
	params.Set("lang", "en-vi")
	params.Set("text", word)
	params.Set("key", YANDEX_TRANSLATOR_APIKEY)

	resp, err := http.PostForm(YURL, params)
	if err != nil {
		util.LogError("yandex", 0, err)
		return "", err
	}

	defer resp.Body.Close()

	meJSON, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		util.LogError("yandex", 1, err)
		return "", err
	}

	var result Result

	err = json.Unmarshal(meJSON, &result)
	if err != nil {
		util.LogError("yandex", 2, err)
		return "", err
	}

	definition := strings.Join(result.Text, " ")
	if definition == word {
		return "", errors.New("sameWord")
	}
	return "-> " + definition, nil
}

func GetName(sender telebot.Chat) string {
	name := sender.FirstName + " " + sender.LastName
	if sender.IsGroupChat() {
		name = sender.Title
	}
	return name
}

func SetWord(word, def string) {
	err := redis_client.Set(word, def, 0).Err()
	if err != nil {
		log.Errorln(err.Error())
	}
}
