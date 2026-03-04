# imgproxy

кеширующий прокси для flixcdn

оригинал ищет в бд по нескольким таблицам

оригинал загружает в s3

ресайз тоже загружает в s3

## env

- `S3_INIT_CHECK` (default: `true`) — проверка доступа к S3 при старте приложения.
	- `false` — отключить init-check (удобно для dev).
	- при включенной проверке отсутствие прав `Read/Write` останавливает запуск,
		отсутствие права `Head` только логируется и не блокирует старт.


по высоте
https://imgproxy.imgproxy.orb.local/sss/videos/1/abef0f58745b022a79cbb545d576f1e3@h600

по ширине
https://imgproxy.imgproxy.orb.local/sss/videos/1/abef0f58745b022a79cbb545d576f1e3@600

