ALTER TABLE checkins
  ADD COLUMN photo_data MEDIUMBLOB NULL COMMENT '拍照打卡原图（二进制，MySQL 存储）' AFTER media_tos_key,
  ADD COLUMN photo_content_type VARCHAR(64) NULL COMMENT '拍照打卡图片 MIME 类型' AFTER photo_data;
