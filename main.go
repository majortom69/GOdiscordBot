package main

import (
	"context"

	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
	"io"
	"layeh.com/gopus"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
)

var (
	GuildID        string = ""
	AppID          string = "982304558522957914"
	RemoveCommands bool   = true
)

const (
	CHANNELS   int = 2
	FRAME_RATE int = 48000
	FRAME_SIZE int = 960
	MAX_BYTES  int = (FRAME_SIZE * 2) * 2
)

type Song struct {
	Media string
	Title string
	//Duraion *string
	Id string
}

// РАБОТА С ОЧЕРЕДЬЮ
type SongQueue struct {
	list    []Song
	current *Song
	Running bool
}

func (queue *SongQueue) Add(song Song) {
	queue.list = append(queue.list, song)
}

func (queue *SongQueue) Next() Song {
	song := queue.list[0]
	queue.list = queue.list[1:]
	queue.current = &song
	return song
}
func (queue SongQueue) HasNext() bool {
	return len(queue.list) > 0
}

// func (queue *SongQueue) Play() {
// 	queue.Running = true
// 	for queue.Running {
// 		song := queue.Next()
// 	}
//
// 	if !queue.Running {
// 		// stop playing
// 	} else {
// 		// finidh queue
// 	}
// }

//	type Queue struct {
//		id int,
//		song Song,
//
// }
// newsong constructs a song instance
func NewSong(media, title, id string) *Song {
	return &Song{Media: media, Title: title, Id: id}
}

const ffmpegPath string = "C:\\Users\\vladislav\\Desktop\\downgradbot\\ffmpeg.exe"

// func (song *Song) Ffmpeg() *exec.Cmd {
// 	return exec.Command(
// 		ffmpegPath,
// 		"-i", song.Media,
// 		"-f", "s16le",
// 		"-ar", strconv.Itoa(FRAME_RATE),
// 		"-ac", strconv.Itoa(CHANNELS),
// 		"pipe:1",
// 	)
// }

type Connection struct {
	voiceConnection *discordgo.VoiceConnection
	send            chan []int16
	lock            sync.Mutex
	sendpcm         bool
	stopRunning     bool
	playing         bool
}

func NewConnection(voiceConnection *discordgo.VoiceConnection) *Connection {
	connection := new(Connection)
	connection.voiceConnection = voiceConnection
	return connection
}

func (connection Connection) Disconnect() {

	connection.voiceConnection.Disconnect()
}

func (connection *Connection) sendPCM(voice *discordgo.VoiceConnection, pcm <-chan []int16) {
	connection.lock.Lock()
	if connection.sendpcm || pcm == nil {
		connection.lock.Unlock()
		return
	}

	connection.sendpcm = true
	connection.lock.Unlock()
	defer func() {
		connection.sendpcm = false
	}()

	encoder, err := gopus.NewEncoder(FRAME_RATE, CHANNELS, gopus.Audio)
	if err != nil {
		log.Printf("[!]Encoder error ", err)
	}

	for {
		receive, ok := <-pcm
		if !ok {
			log.Println("[!] DPC channel closed ")
			return
		}
		opus, err := encoder.Encode(receive, FRAME_SIZE, MAX_BYTES)
		if err != nil {
			log.Println("[!] Encoding error ", err)
		}
		if !voice.Ready || voice.OpusSend == nil {
			log.Printf("\n[!] Discordgo not ready for opus packets %v : %+v", voice.Ready, voice.OpusSend)
			return
		}
		voice.OpusSend <- opus

	}
}

func (connection Connection) Play(ffmpeg *exec.Cmd) error {
	if connection.playing {
		return errors.New("song already playing")
	}
	connection.stopRunning = false
	out, err := ffmpeg.StdoutPipe()
	if err != nil {
		return err
	}
	buffer := bufio.NewReaderSize(out, 16384)
	err = ffmpeg.Start()
	if err != nil {
		return err
	}
	connection.playing = true
	defer func() {
		connection.playing = false
	}()
	connection.voiceConnection.Speaking(true)
	defer connection.voiceConnection.Speaking(false)
	if connection.send == nil {
		connection.send = make(chan []int16, 2)
	}
	go connection.sendPCM(connection.voiceConnection, connection.send)
	for {
		if connection.stopRunning {
			ffmpeg.Process.Kill()
			break
		}
		audioBuffer := make([]int16, FRAME_SIZE*CHANNELS)
		err = binary.Read(buffer, binary.LittleEndian, &audioBuffer)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
		connection.send <- audioBuffer
	}
	return nil
}

func (connection *Connection) Stop() {
	connection.stopRunning = true
	//connection.playing = false
}

const googleDeveloperKey = "AIzaSyAZ01JvdUKpo2pKhQZ5syUYtj7entsUGgg"

func getUserVCID(s *discordgo.Session, guildID, userID string) (string, error) {
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return "", err
	}

	//
	for _, vs := range guild.VoiceStates {
		if vs.UserID == userID {
			return vs.ChannelID, nil
		}
	}

	return "", fmt.Errorf("user is not in  a voice channel")

}

var ( //Список комманд и их описание( чисто чтобы отображались в дс)
	dmPermission            bool      = false
	defaultMemberPermission int64     = discordgo.PermissionSendMessages
	voicePerms              int64     = discordgo.PermissionVoiceConnect
	sq                      SongQueue = SongQueue{}

	commands = []*discordgo.ApplicationCommand{
		{
			Name:                     "ping",
			Description:              "пингануть бота",
			DefaultMemberPermissions: &defaultMemberPermission,
			DMPermission:             &dmPermission,
		},
		{
			Name:                     "play",
			Description:              "Добавить трек в очередь",
			DefaultMemberPermissions: &voicePerms,
			DMPermission:             &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{

					Name:        "query",
					Description: "Ссылка на трек с Youtube/Spotify или название видео с Youtube",
					Type:        discordgo.ApplicationCommandOptionString,
					Required:    true,
				},
			},
		},
		{
			Name:                     "stop",
			Description:              "Отсановить",
			DefaultMemberPermissions: &defaultMemberPermission,
			DMPermission:             &dmPermission,
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"ping": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				// эта хряня нужна чтобы отображалось что ввел юзер
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Nasral po polnoy",
				},
			})
		},

		"play": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			options := i.ApplicationCommandData().Options
			var query string
			var ytVideoLink string
			// конвертируем слайс в мап
			optionMap := make(map[string]*discordgo.ApplicationCommandInteractionDataOption, len(options))
			for _, opt := range options {
				optionMap[opt.Name] = opt
			}

			if option, ok := optionMap["query"]; ok {
				query += option.StringValue()
			}

			var stringResult string
			// Получаем рузальтаты по запросу с ютуба
			ctx := context.Background()
			yts, err := youtube.NewService(ctx, option.WithAPIKey(googleDeveloperKey))
			if err != nil {
				log.Printf("[!] Error creating youtube client %v", err)
			}

			fmt.Println("[DEBUG] query =", query)
			results := YTSearch(query, yts)
			fmt.Println(results)
			// Сейвим ссылку + название
			for id, title := range results {
				ytVideoLink += "https://www.youtube.com/watch?v=" + id
				stringResult = title
			}

			var song Song = Song{ytVideoLink, stringResult, "1"}

			vcid, err := getUserVCID(s, i.GuildID, i.Member.User.ID)
			if err != nil || vcid == "" {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "❌ Пoльзователь должен быть в голосовом канале",
					},
				})
				log.Println("[DEBUG] vcid ", vcid)

				//   ...
				return
			}

			sq.Add(song)
			if len(sq.list) == 1 {

				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: fmt.Sprintf("<:Youtube:1360579220878655579> Сейчас играет: %v", stringResult),
					},
				})

			} else {
				sq.Add(song)
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: fmt.Sprintf("<:Youtube:1360579220878655579> Добавлено в очередь: %v", stringResult),
					},
				})
				return
			}

			vc, err := s.ChannelVoiceJoin(i.GuildID, vcid, false, true)
			//vc, err := s.ChannelVoiceJoin((i.GuildID,)
			if err != nil {
				log.Printf("[!] Error joining channel %v", err)
				return
			}

			ff := exec.Command("C:\\Users\\vladislav\\Desktop\\downgradbot\\ffmpeg.exe",
				"-i", "pipe:0",
				"-f", "s16le",
				"-ar", strconv.Itoa(FRAME_RATE),
				"-ac", strconv.Itoa(CHANNELS),
				"pipe:1",
			)

			if !sq.Running {
				sq.Running = true
				go func() {
					defer func() {
						sq.Running = false
						vc.Close()
					}()

					for sq.HasNext() {

						currSong := sq.Next()
						yt := exec.Command("C:\\Users\\vladislav\\Desktop\\downgradbot\\yt-dlp.exe",
							"-f",
							"bestaudio",
							"-o",
							"-",
							currSong.Media,
						)
						ytOut, err := yt.StdoutPipe()
						if err != nil {
							log.Println("[!] Error creatng yt-dlp pipe %v", err)
							return
						}
						ff.Stdin = ytOut

						if err := yt.Start(); err != nil {
							log.Println("[!] Eroro staring yt-dlp ", err)
							return
						}

						conn := NewConnection(vc)
						//song := NewSong(ytVideoLink, stringResult, "1")
						if err := conn.Play(ff); err != nil {
							log.Println("[!] Playback error %v", err)
						}

					}

				}()
			}

			//defer vc.Close()

		},
	}
)

var (
	//query      string = "MrBeast"
	maxResults int64 = 1
)

func YTSearch(query string, yts *youtube.Service) map[string]string {

	call := yts.Search.List([]string{"id,snippet"}).Q(query).MaxResults(maxResults).Type("video")
	response, err := call.Do()
	if err != nil {
		log.Printf("\n[!] Error doing  a search")
	}

	// uhegbhetv  видосы, каналы, плейлисты в отдельные списки
	videos := make(map[string]string)
	//channels := make(map[string]string)
	//playlists := make(map[string]string)
	fmt.Println("[DEBUG] raw response items count:", len(response.Items))
	for _, item := range response.Items {
		fmt.Println("[DEBUG] item kind:", item.Id.Kind)
		fmt.Println("[DEBUG] item title:", item.Snippet.Title)
		switch item.Id.Kind {
		case "youtube#video":
			videos[item.Id.VideoId] = item.Snippet.Title
			fmt.Println(item.Snippet.Title)
		}
	}
	return videos
}

func main() {

	log.Printf("[DEBUG] %v\n", runtime.GOOS)
	discord, err := discordgo.New("Bot " + "OTgyMzA0NTU4NTIyOTU3OTE0.GTBxsR.fJiNLEWukBzhoZVyjJcol3aAmYtwwDv7lOH35Q")
	if err != nil {
		fmt.Println(err)
		return
	}

	discord.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {

		if m.Content == "rmrf" {
			s.ChannelMessageSend(m.ChannelID, "я тебе щас башку снесу клоп ебанный")
		}
	})
	discord.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("[+] Logged in as %v", s.State.User.Username)
	})
	discord.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	})

	_, err = discord.ApplicationCommandBulkOverwrite(AppID, GuildID, commands)
	if err != nil {
		log.Printf("[!] Error while updating commands has occured %v", err)
	}
	//discord.Identify.Intents = discordgo.IntentsAllWithoutPrivileged

	err = discord.Open()
	if err != nil {
		fmt.Println(err)
	}

	log.Println("[+] Adding commands")
	registeredCommands := make([]*discordgo.ApplicationCommand, len(commands))
	for i, v := range commands {
		cmd, err := discord.ApplicationCommandCreate(discord.State.User.ID, GuildID, v)
		if err != nil {
			log.Printf("\n[!] Cannot create command %v ,error %v", v.Name, err)
		}
		registeredCommands[i] = cmd
	}
	defer discord.Close()

	// Ждем когда поступит сигнал  в канал(os.Interrupt = Ctrl + C)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	log.Println("[!] Press Ctrl+C to exit")
	// Пока не поступит сигнал, не продолжать
	<-stop

	// Удаляем те комманды , которые мы добавили
	// Очитка идет по желанию
	// if RemoveCommands {
	// 	for _, v := range registeredCommands {
	// 		err := discord.ApplicationCommandDelete(discord.State.User.ID, GuildID, v.ID)
	// 		if err != nil {
	// 			log.Printf("[!] Cannot delete command %v, error %v", v.Name, err)
	// 		}
	// 	}
	// }

	log.Println("[-] Succesfully shutting down")
}
