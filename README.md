# Простой муыкальный бот для Discord на GO 
## Функции бота
- Добавить трек в очередь (по названию или ссылки с Youtube)
- Пропустить трек
- Поставить трек на пазуу

## Сборка проекта
Для сборки и запуска проекта на системе  должны быть установелнны утилиты: [ffmpeg](https://github.com/FFmpeg/FFmpeg) и [yt-dlp](https://github.com/yt-dlp/yt-dlp)

сборка и запуск(Linux)
```sh
go mod tidy
go build main.go
chmod +x ./main
./main
```
Скриншот с примером использования
![image](https://github.com/user-attachments/assets/0c4981d4-c397-417a-ad39-bc62059bc2ae)

  
