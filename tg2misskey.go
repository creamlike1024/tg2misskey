package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
	mi "github.com/yitsushi/go-misskey"
	"github.com/yitsushi/go-misskey/core"
	"github.com/yitsushi/go-misskey/models"
	"github.com/yitsushi/go-misskey/services/drive/files"
	"github.com/yitsushi/go-misskey/services/drive/folders"
	"github.com/yitsushi/go-misskey/services/notes"
)

type View struct {
	localOnly  bool
	visibility models.Visibility
}

func main() {
	startTime := time.Now()
	// 检查配置文件是否存在，如果不存在则直接读取环境变量
	if _, err := os.Stat("config.env"); os.IsNotExist(err) {
		log.Info("config.env file not found, reading from environment variables")
		if os.Getenv("MISSKEY_URL") == "" || os.Getenv("MISSKEY_TOKEN") == "" || os.Getenv("TELEGRAM_BOT_TOKEN") == "" {
			log.Fatal("MISSKEY_URL, MISSKEY_TOKEN and TELEGRAM_BOT_TOKEN must be set")
		}
	} else {
		err := godotenv.Load("config.env")
		if err != nil {
			log.Fatal("Error loading config.env file")
		}
	}
	// misskey
	client, err := mi.NewClientWithOptions(mi.WithSimpleConfig(os.Getenv("MISSKEY_URL"), os.Getenv("MISSKEY_TOKEN")))
	if err != nil {
		log.Fatal(err)
	}

	// client.LogLevel(log.InfoLevel)

	var v View
	if os.Getenv("LOCAL_ONLY") == "true" {
		v.localOnly = true
	} else if os.Getenv("LOCAL_ONLY") == "false" {
		v.localOnly = false
	} else {
		log.Fatal("LOCAL_ONLY value can only be set to true or false")
	}
	switch os.Getenv("VISIBILITY") {
	case "public":
		v.visibility = models.VisibilityPublic
	case "home":
		v.visibility = models.VisibilityHome
	case "followers":
		v.visibility = models.VisibilityFollowers
	default:
		log.Fatal("VISIBILITY value can only be set to public, home or followers")
	}
	if findFolder(client, os.Getenv("UPLOAD_FOLDER")) == "" {
		createFolder(client, os.Getenv("UPLOAD_FOLDER"))
	}

	// tgbot
	bot, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if err != nil {
		log.Panic(err)
	}
	log.Info("Authorized on account ", bot.Self.UserName)
	var chatId int64
	var userId int64
	if os.Getenv("TELEGRAM_CHAT_ID") == "" {
		chatId = 0
	} else {
		chatId, err = strconv.ParseInt(os.Getenv("TELEGRAM_CHAT_ID"), 10, 64)
		if err != nil {
			log.Fatal("Invalid TELEGRAM_CHAT_ID")
		}
	}
	if os.Getenv("TELEGRAM_USER_ID") == "" {
		userId = 0
	} else {
		userId, err = strconv.ParseInt(os.Getenv("TELEGRAM_USER_ID"), 10, 64)
		if err != nil {
			log.Fatal("Invalid TELEGRAM_USER_ID")
		}
	}
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updatesChannel := bot.GetUpdatesChan(u)

	var fileIDs []string
	var msg string

	for update := range updatesChannel {
		// 只处理启动后，不为空，且来自指定 chatId 或 userId 的消息
		if update.Message == nil || update.Message.Time().Before(startTime) {
			continue
		}
		if (chatId != 0 && update.Message.Chat.ID != chatId) || (userId != 0 && update.Message.From.ID != userId) {
			continue
		}
		if update.Message.IsCommand() {
			// 处理命令
			help := func() {
				text := "Hello! I'm a bot that forwards Telegram messages to Misskey.\n\n"
				text += "Send me a message or forward a message to me and I'll forward it to Misskey.\n\n"
				tgmsg := tgbotapi.NewMessage(update.Message.Chat.ID, text)
				tgmsg.ReplyToMessageID = update.Message.MessageID
				_, _ = bot.Send(tgmsg)
			}
			switch update.Message.Command() {
			case "start":
				help()
			case "help":
				help()
			default:
				continue
			}
		}
		if update.Message.Photo != nil {
			for i, photo := range update.Message.Photo {
				// 只上传最高画质的图片，即 slice 里的最后一个元素
				if i == len(update.Message.Photo)-1 {
					fileUrl, err := bot.GetFileDirectURL(photo.FileID)
					if err != nil {
						log.Error(err)
					} else {
						fillMsgAndFileIDs(client, &fileIDs, &msg, update.Message.Caption, fileUrl)
					}
				}
			}
			if len(updatesChannel) == 0 {
				addFootInfo(&msg, update.Message)
				sendWithAttachment(client, v, &msg, &fileIDs)
			}
		} else if update.Message.Video != nil {
			fileUrl, err := bot.GetFileDirectURL(update.Message.Document.FileID)
			if err != nil {
				log.Error(err)
				continue
			}
			fillMsgAndFileIDs(client, &fileIDs, &msg, update.Message.Caption, fileUrl)
			if len(updatesChannel) == 0 {
				addFootInfo(&msg, update.Message)
				sendWithAttachment(client, v, &msg, &fileIDs)
			}
		} else if update.Message.Audio != nil {
			fileUrl, err := bot.GetFileDirectURL(update.Message.Document.FileID)
			if err != nil {
				log.Error(err)
				continue
			}
			fillMsgAndFileIDs(client, &fileIDs, &msg, update.Message.Caption, fileUrl)
			if len(updatesChannel) == 0 {
				addFootInfo(&msg, update.Message)
				sendWithAttachment(client, v, &msg, &fileIDs)
			}
		} else if update.Message.Document != nil {
			fileUrl, err := bot.GetFileDirectURL(update.Message.Document.FileID)
			if err != nil {
				log.Error(err)
				continue
			}
			fillMsgAndFileIDs(client, &fileIDs, &msg, update.Message.Caption, fileUrl)
			if len(updatesChannel) == 0 {
				addFootInfo(&msg, update.Message)
				sendWithAttachment(client, v, &msg, &fileIDs)
			}
		} else {
			msg = update.Message.Text
			addFootInfo(&msg, update.Message)
			sendMiNote(client, v, &msg, nil)
		}
	}
}

func addFootInfo(msg *string, tgMsg *tgbotapi.Message) {
	*msg += "\n\n"
	if tgMsg.ForwardFromChat != nil {
		*msg += fmt.Sprintf("Forwarded from Telegram Channel \"%s\" `@%s`\n", tgMsg.ForwardFromChat.Title, tgMsg.ForwardFromChat.UserName)
	}
	*msg += os.Getenv("FOOT_INFO")
}

func sendMiNote(client *mi.Client, v View, msg *string, fileIDs []string) {
	resp, err := client.Notes().Create(notes.CreateRequest{
		Text:       core.NewString(*msg),
		Visibility: v.visibility,
		LocalOnly:  v.localOnly,
		FileIDs:    fileIDs,
	})
	if err != nil {
		log.Errorf("[Notes] Error happened: %s", err)
		return
	}
	log.Infof("Note: %s Created", resp.CreatedNote.ID)
}

func uploadFile(client *mi.Client, fileURL string) string {
	file, err := client.Drive().File().CreateFromURL(files.CreateFromURLOptions{
		Name:     strconv.FormatInt(time.Now().UnixNano(), 10) + filepath.Ext(fileURL),
		URL:      fileURL,
		FolderID: findFolder(client, os.Getenv("UPLOAD_FOLDER")),
	})
	if err != nil {
		log.Errorf("[Drive/File/CreateFromURL] %s", err)
		return ""
	}
	log.Info("File: " + *file.Name + " with ID: " + file.ID + " uploaded")
	return file.ID
}

func findFolder(client *mi.Client, name string) string {
	folderList, err := client.Drive().Folder().Find(folders.FindRequest{
		Name: name,
	})
	if err != nil {
		log.Errorf("[Drive/Folder/Find] %s", err)
		return ""
	}
	for _, folder := range folderList {
		if folder.Name == name {
			return folder.ID
		}
	}
	return ""
}

func createFolder(client *mi.Client, name string) string {
	folder, err := client.Drive().Folder().Create(folders.CreateRequest{
		Name: name,
	})
	if err != nil {
		log.Errorf("[Drive/Folder/Create] %s", err)
		return ""
	}
	log.Info("Folder: " + folder.Name + " with ID: " + folder.ID + " created")
	return folder.ID
}

func fillMsgAndFileIDs(client *mi.Client, fileIDs *[]string, msg *string, caption string, fileURL string) {
	*fileIDs = append(*fileIDs, uploadFile(client, fileURL))
	if caption != "" {
		*msg = caption
	}
}

func sendWithAttachment(client *mi.Client, v View, msg *string, fileIDs *[]string) {
	sendMiNote(client, v, msg, *fileIDs)
	*fileIDs = nil
	*msg = ""
}
