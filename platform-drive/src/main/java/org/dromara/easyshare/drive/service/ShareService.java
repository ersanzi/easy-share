package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.toolkit.Wrappers;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsShare;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.dromara.easyshare.drive.mapper.EsShareMapper;
import org.springframework.stereotype.Service;

import java.security.SecureRandom;
import java.time.LocalDateTime;
import java.util.List;

/**
 * 文件分享：创建 / 列表 / 撤销 / 兑换。
 *
 * <p>与预签名 URL 的本质区别是**可撤销**：预签名一旦签发就只能等它过期，
 * 而分享是一条记录，撤销立刻生效。企业场景里「发错了想收回」是高频需求，
 * 这才是分享层的存在理由。
 *
 * @author EasyShare
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class ShareService {

    /** token 字符集：URL 安全，不含 + / = */
    private static final String ALPHABET = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz";

    /** 22 位 base62 ≈ 128 bit 熵，不可枚举 */
    private static final int TOKEN_LENGTH = 22;

    /** 默认有效期：7 天。太短失去「发给外部」的意义，太长等于永久外链 */
    static final int DEFAULT_EXPIRES_HOURS = 24 * 7;

    /** 上限 30 天：与回收站保留期同量级，超过这个长度就该走别的协作方式 */
    static final int MAX_EXPIRES_HOURS = 24 * 30;

    private final EsShareMapper shareMapper;
    private final EsFileMapper fileMapper;
    private final SecureRandom random = new SecureRandom();

    /**
     * 创建一条分享。
     *
     * <p>文件名与大小在这里做快照：分享记录要能独立成立，不能依赖文件行还在。
     *
     * @param expiresHours 期望有效期（小时）；空或非正数走默认，超过上限被截断
     */
    public EsShare create(Long userId, String spaceType, String path, Integer expiresHours) {
        int hours = expiresHours == null || expiresHours <= 0
            ? DEFAULT_EXPIRES_HOURS : Math.min(expiresHours, MAX_EXPIRES_HOURS);
        EsFile row = fileMapper.selectOne(Wrappers.<EsFile>lambdaQuery()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerOf(spaceType, userId))
            .eq(EsFile::getFilePath, path)
            .eq(EsFile::getState, EsFile.STATE_ACTIVE));

        EsShare share = new EsShare();
        share.setToken(randomToken());
        share.setOwnerId(userId);
        share.setSpaceType(spaceType);
        share.setFilePath(path);
        // 存量未登记对象查不到行：文件名从路径末段取，大小记 0，不编造
        share.setFileName(row != null && row.getFileName() != null
            ? row.getFileName() : lastSegment(path));
        share.setFileSize(row != null && row.getFileSize() != null ? row.getFileSize() : 0L);
        share.setExpiresAt(LocalDateTime.now().plusHours(hours));
        share.setRevoked(false);
        share.setVisitCount(0);
        share.setCreatedAt(LocalDateTime.now());
        shareMapper.insert(share);
        return share;
    }

    /** 我创建的分享，按时间倒序 */
    public List<EsShare> listMine(Long userId) {
        return shareMapper.selectList(Wrappers.<EsShare>lambdaQuery()
            .eq(EsShare::getOwnerId, userId)
            .orderByDesc(EsShare::getCreatedAt));
    }

    /**
     * 撤销分享。
     *
     * @return true=确实撤销了一条；false=不存在或不属于该用户
     */
    public boolean revoke(Long userId, Long shareId) {
        // 直接按 owner 条件更新，避免「先查再改」之间被换掉
        int changed = shareMapper.update(null, Wrappers.<EsShare>lambdaUpdate()
            .eq(EsShare::getShareId, shareId)
            .eq(EsShare::getOwnerId, userId)
            .set(EsShare::getRevoked, true));
        return changed > 0;
    }

    /**
     * 用 token 兑换分享记录。
     *
     * @return 有效则返回记录；不存在 / 已撤销 / 已过期一律 null
     */
    public EsShare resolve(String token) {
        if (token == null || token.isBlank()) {
            return null;
        }
        EsShare share = shareMapper.selectOne(Wrappers.<EsShare>lambdaQuery()
            .eq(EsShare::getToken, token));
        if (share == null || !share.usableAt(LocalDateTime.now())) {
            return null;
        }
        return share;
    }

    /** 访问计数 +1。302 计数不是精确审计，只用于「这条链接有没有人用过」 */
    public void recordVisit(Long shareId) {
        shareMapper.update(null, Wrappers.<EsShare>lambdaUpdate()
            .eq(EsShare::getShareId, shareId)
            .setSql("visit_count = visit_count + 1"));
    }

    private static Long ownerOf(String spaceType, Long userId) {
        return org.dromara.easyshare.drive.domain.EsSpace.TYPE_SHARED.equals(spaceType)
            ? EsFile.SHARED_OWNER : userId;
    }

    private static String lastSegment(String path) {
        int cut = path.lastIndexOf('/');
        return cut >= 0 ? path.substring(cut + 1) : path;
    }

    private String randomToken() {
        StringBuilder sb = new StringBuilder(TOKEN_LENGTH);
        for (int i = 0; i < TOKEN_LENGTH; i++) {
            sb.append(ALPHABET.charAt(random.nextInt(ALPHABET.length())));
        }
        return sb.toString();
    }
}
