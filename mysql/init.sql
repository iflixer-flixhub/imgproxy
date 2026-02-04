-- база/юзер задаются в docker-compose env, тут только таблицы

USE imgproxy-db;


CREATE TABLE IF NOT EXISTS actors (
  id int unsigned NOT NULL AUTO_INCREMENT,
  poster_url varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  PRIMARY KEY (id)
);
INSERT INTO `actors` (`id`, `poster_url`) VALUES
(1, 'https://st.kp.yandex.net/images/actor_iphone/iphone360_3759273.jpg'),
(2, 'https://st.kp.yandex.net/images/actor_iphone/iphone360_10342606.jpg'),
(3, 'https://st.kp.yandex.net/images/actor_iphone/iphone360_5223523.jpg');




CREATE TABLE IF NOT EXISTS videos (
  id int NOT NULL AUTO_INCREMENT,
  img varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '',
  backdrop varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '',
  PRIMARY KEY (id)
);
INSERT INTO `videos` (`id`, `img`, `backdrop`) VALUES
(1, 'https://kinopoiskapiunofficial.tech/images/posters/kp/501333.jpg', 'https://img0.flixcdn.space/videos/1/orig_989373289649fe1d838fc7d99fdfb106'),
(2, 'https://kinopoiskapiunofficial.tech/images/posters/kp/781898.jpg', 'https://image.tmdb.org/t/p/w500/bkhhRVrBEEAWn80qBqUGz2dzIoB.jpg'),
(3, 'https://kinopoiskapiunofficial.tech/images/posters/kp/733493.jpg', 'https://assets.fanart.tv/fanart/tv/268592/showbackground/the-100-54127323d76c4.jpg');



CREATE TABLE IF NOT EXISTS directors (
  id int unsigned NOT NULL AUTO_INCREMENT,
  poster_url varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  PRIMARY KEY (id)
);
INSERT INTO `directors` (`id`, `poster_url`) VALUES
(1, 'https://st.kp.yandex.net/images/actor_iphone/iphone360_3368745.jpg'),
(2, 'https://st.kp.yandex.net/images/actor_iphone/iphone360_3258751.jpg'),
(3, 'https://st.kp.yandex.net/images/actor_iphone/iphone360_1331451.jpg');


CREATE TABLE IF NOT EXISTS screenshots (
  id int NOT NULL AUTO_INCREMENT,
  url varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
  PRIMARY KEY (id)
);
INSERT INTO `screenshots` (`id`, `url`) VALUES
(1, 'https://storage.kinohd.co/379ddfe5d3459c42686a448993efbf21:2055010101/movies/c7a4d501a626d4c7a991400e198b8d44536d6b29/thumb001.jpg'),
(2, 'https://storage.kinohd.co/7c84bc4024a94ac1291431237e570e52:2055010101/movies/c7a4d501a626d4c7a991400e198b8d44536d6b29/thumb002.jpg'),
(3, 'https://storage.kinohd.co/6ef1180730622cf42f377fcb2400c6b4:2055010101/movies/c7a4d501a626d4c7a991400e198b8d44536d6b29/thumb003.jpg');


