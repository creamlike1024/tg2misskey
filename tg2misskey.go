package main

import (
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
	godotenv.Load("config.env")
	//misskey
	client, err := mi.NewClientWithOptions(mi.WithSimpleConfig(os.Getenv("MISSKEY_URL"), os.Getenv("MISSKEY_TOKEN")))
	if err != nil {
		log.Fatal(err)
	}

	// client.LogLevel(log.DebugLevel)

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

	//tgbot
	bot, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if err != nil {
		log.Panic(err)
	}
	log.Info("Authorized on account ", bot.Self.UserName)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updatesChannel := bot.GetUpdatesChan(u)

	var fileIDs []string
	var msg string

	for update := range updatesChannel {
		if update.Message == nil {
			continue
		}

		if update.Message.Photo != nil {
			for i, photo := range update.Message.Photo {
				// 只上传最高画质的图片，即 slice 里的最后一个元素
				if i == len(update.Message.Photo)-1 {
					fillMsgAndFileIDs(client, &fileIDs, &msg, update.Message.Caption, ignoreError(bot.GetFileDirectURL(photo.FileID)).(string))
				}
			}
			if len(updatesChannel) == 0 {
				sendWithAttachment(client, v, &msg, &fileIDs)
			}
		} else if update.Message.Video != nil {
			fillMsgAndFileIDs(client, &fileIDs, &msg, update.Message.Caption, ignoreError(bot.GetFileDirectURL(update.Message.Video.FileID)).(string))
			if len(updatesChannel) == 0 {
				sendWithAttachment(client, v, &msg, &fileIDs)
			}
		} else if update.Message.Audio != nil {
			fillMsgAndFileIDs(client, &fileIDs, &msg, update.Message.Caption, ignoreError(bot.GetFileDirectURL(update.Message.Audio.FileID)).(string))
			if len(updatesChannel) == 0 {
				sendWithAttachment(client, v, &msg, &fileIDs)
			}
		} else if update.Message.Document != nil {
			fillMsgAndFileIDs(client, &fileIDs, &msg, update.Message.Caption, ignoreError(bot.GetFileDirectURL(update.Message.Document.FileID)).(string))
			if len(updatesChannel) == 0 {
				sendWithAttachment(client, v, &msg, &fileIDs)
			}
		} else {
			sendMiNote(client, v, update.Message.Text, nil)
		}
	}
}

func ignoreError(val interface{}, err error) interface{} {
	return val
}

func addFootInfo(msg string) string {
	return msg + "\n\n" + os.Getenv("FOOT_INFO")
}

func sendMiNote(client *mi.Client, v View, msg string, fileIDs []string) {
	resp, err := client.Notes().Create(notes.CreateRequest{
		Text:       core.NewString(addFootInfo(msg)),
		Visibility: v.visibility,
		LocalOnly:  v.localOnly,
		FileIDs:    fileIDs,
	})
	if err != nil {
		log.Error("[Notes] Error happened: %s", err)
		return
	}
	log.Info("Note: " + resp.CreatedNote.ID + " Created")
}

func uploadFile(client *mi.Client, fileURL string) string {
	file, err := client.Drive().File().CreateFromURL(files.CreateFromURLOptions{
		Name:     strconv.FormatInt(time.Now().UnixNano(), 10) + filepath.Ext(fileURL),
		URL:      fileURL,
		FolderID: findFolder(client, os.Getenv("UPLOAD_FOLDER")),
	})
	if err != nil {
		log.Error("[Drive/File/CreateFromURL] %s", err)
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
		log.Error("[Drive/Folder/Find] %s", err)
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
		log.Error("[Drive/Folder/Create] %s", err)
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
	sendMiNote(client, v, *msg, *fileIDs)
	*fileIDs = nil
	*msg = ""
}
