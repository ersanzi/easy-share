package org.dromara.easyshare.drive;

import java.time.Instant;

/**
 * 用户空间内的一个对象。
 * <p>
 * {@code path} 是**相对路径**（已剥离 {@code users/{userId}/} 前缀），
 * 客户端只认相对路径，用户命名空间对客户端不可见也不可指定。
 * 不返回 contentType：S3 ListObjectsV2 本就不含该字段，客户端按扩展名推断即可。
 *
 * @param path         相对路径，如 photos/2024/img.jpg
 * @param size         字节数
 * @param lastModified 最后修改时间
 * @author EasyShare
 */
public record DriveObject(String path, long size, Instant lastModified) {
}
