package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.DriveKeys;
import org.dromara.easyshare.drive.DriveObject;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsFileChangeLog;
import org.dromara.easyshare.drive.domain.EsSpace;
import org.dromara.easyshare.drive.domain.EsSpaceMember;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.springframework.dao.DuplicateKeyException;
import org.springframework.stereotype.Service;

import java.time.LocalDateTime;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;

/**
 * 文件目录层服务：稳定 fileId 与空间内路径的绑定、列表回填与惰性补账。
 * <p>
 * 登记时机（控制面不在上传数据路径上，见 {@link EsFile} 类注）：
 * <ul>
 *   <li>presignPut 签发时 upsert——多数文件自此有稳定身份；</li>
 *   <li>列表时惰性补账——目录层上线前的存量对象在第一次被列举时自愈归账。</li>
 * </ul>
 * 本表不是存在性真相源：幽灵行（登记后从未写入）不影响列表正确性，
 * 会在删除或后续对账切片中清理。
 *
 * @author EasyShare
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class DriveFileService {

    private final EsFileMapper fileMapper;
    private final DriveChangeLogService changeLog;

    /**
     * **只校验、不登记**：确认调用方看到的版本与服务端当前版本一致。
     *
     * <p>为什么需要单独一个方法：单请求上传（{@code /presign-put}）可以在签发时就登记，
     * 因为签发那一刻就要判配额；而 Multipart 上传走的是「create → 逐分片 → complete」，
     * 真正的写入发生在 **complete**，登记也发生在 complete。若只在 create 校验，
     * 客户端的「我看到的版本」与「写入发生时服务端的版本」之间隔着整个上传时长
     * （大文件可能几分钟），校验会**形同虚设**——它证明的只是「开始时版本一致」。
     *
     * <p>因此 Multipart 的校验点在 **complete**：调用方在 create 时把版本寄存在会话上，
     * complete 时由本方法比对，再交给 registerOnPresign 做条件更新。
     *
     * @param expectedVersion 调用方看到的版本；null = 不校验（向后兼容）
     */
    public void assertVersion(String spaceType, Long ownerId, String relativePath, Long expectedVersion) {
        if (expectedVersion == null) {
            return; // 旧调用方：行为与引入 CAS 之前一致
        }
        String path = DriveKeys.normalizeRelative(relativePath);
        EsFile existing = selectActiveByPath(spaceType, ownerId, path);
        if (existing == null) {
            if (expectedVersion > 0L) {
                throw new ServiceException(
                    "文件已被删除（你看到的版本 " + expectedVersion + "），请刷新后再试");
            }
            return; // 期望「不存在」且确实不存在：可以写
        }
        long current = normalizedVersion(existing);
        if (expectedVersion != current) {
            throw new ServiceException(String.format(
                "文件已被其他人修改（你看到的版本 %d，当前版本 %d），已拒绝覆盖以免丢失对方的内容；"
                    + "请刷新后重试", expectedVersion, current));
        }
    }

    /**
     * presignPut 签发时登记（幂等 upsert）。
     *
     * @param spaceType    personal / shared
     * @param ownerId      空间归属（personal=用户，shared=0）
     * @param uploadBy     真实上传者
     * @param relativePath 客户端相对路径（内部规范化）
     * @param declaredSize 申报字节数（可能为 0/未知，保持原值不覆盖）
     */
    public void registerOnPresign(String spaceType, Long ownerId, Long uploadBy,
                                  String relativePath, long declaredSize) {
        registerOnPresign(spaceType, ownerId, uploadBy, relativePath, declaredSize, null);
    }

    /**
     * 登记上传意图（带**乐观并发校验**，路线图切片 1 的 CAS）。
     *
     * <p><b>为什么必须有它</b>：没有 CAS 时，两个客户端先后写同一路径是**静默覆盖**——
     * 后写的赢，先写的内容连同任何提示一起消失。2026-10-16 在真机端到端实测确认过
     * （见路线图切片 1 的「实测确认」）：A 写入成功（PUT 200），B 随后写入成功，
     * 最终只剩 B 的内容。这正是钉盘 `preDentryVersion` 要解决的问题。
     *
     * <p><b>判据</b>（对标钉盘取证 §3 的 `preDentryVersion`）：
     * 调用方带来「我看到的是哪个版本」，服务端比对当前版本，不匹配就拒绝，
     * 让用户自己决定怎么办——**绝不静默覆盖**。
     *
     * @param expectedVersion 调用方看到的版本号；**null 表示不做校验**（旧调用方/新建场景，
     *                        行为与改造前一致）；0 表示「我认为这是首次登记」
     */
    public void registerOnPresign(String spaceType, Long ownerId, Long uploadBy,
                                  String relativePath, long declaredSize, Long expectedVersion) {
        registerOnPresign(spaceType, ownerId, uploadBy, relativePath, declaredSize, expectedVersion, null);
    }

    /**
     * 登记上传意图（带内容哈希，切片 6）。
     *
     * <p>哈希与版本一起在**签发时**落行：秒传命中时这条登记就是唯一落点
     * （字节路径被整个跳过），此时哈希必须已经可靠地记在行上，
     * 否则下一次上传无法用这份内容做秒传判定。
     *
     * @param contentMd5 客户端整文件读算的十六进制小写 MD5；null = 未上报（行为与旧调用方一致）
     */
    public void registerOnPresign(String spaceType, Long ownerId, Long uploadBy,
                                  String relativePath, long declaredSize, Long expectedVersion,
                                  String contentMd5) {
        String path = DriveKeys.normalizeRelative(relativePath);
        EsFile existing = selectActiveByPath(spaceType, ownerId, path);
        if (existing == null) {
            // 调用方以为文件已存在（带了 >0 的期望版本），但目录层里没有——
            // 说明它看到的那份已经被删掉了。这是冲突，不是新建。
            if (expectedVersion != null && expectedVersion > 0L) {
                throw new ServiceException(
                    "文件已被删除（你看到的版本 " + expectedVersion + "），请刷新后再试");
            }
            insertSafely(spaceType, ownerId, uploadBy, path, declaredSize, contentMd5);
            return;
        }
        long current = normalizedVersion(existing);
        if (expectedVersion != null && expectedVersion != current) {
            throw new ServiceException(String.format(
                "文件已被其他人修改（你看到的版本 %d，当前版本 %d），已拒绝覆盖以免丢失对方的内容；"
                    + "请刷新后重试", expectedVersion, current));
        }
        // 同名覆盖上传 = 新版本：version_id 单调递增（轻口径，版本历史为后续切片）
        // ⚠️ **必须是条件更新，不能是「读出来改再写回」**：
        // 后者在并发下仍然会互相覆盖（两个线程都读到 v3，都写 v4）。
        // 这里把版本判定放进 UPDATE 的 WHERE 里，由数据库保证原子性；
        // 影响行数为 0 即表示「在我读完之后、写之前，有人先改了」。
        var update = new LambdaUpdateWrapper<EsFile>()
            .eq(EsFile::getFileId, existing.getFileId())
            .eq(EsFile::getVersionId, current)
            .set(EsFile::getVersionId, current + 1L);
        // 申报大小 > 0 才覆盖 file_size；为 0 表示「长度未知」，保持原值
        // （与改造前一致：未知大小不该把已知大小冲成 0）
        if (declaredSize > 0) {
            update.set(EsFile::getFileSize, declaredSize);
        }
        if (contentMd5 != null && !contentMd5.isBlank()) {
            update.set(EsFile::getContentMd5, contentMd5)
                .set(EsFile::getMd5Reliable, MD5_RELIABLE_CLIENT);
        }
        int updated = fileMapper.update(null, update);
        if (updated == 0) {
            throw new ServiceException(String.format(
                "文件在本次上传期间被其他人修改（版本 %d 已过期），已拒绝覆盖；请刷新后重试", current));
        }
        // 同名覆盖 = updated 事件；fileId 与路径都不变，版本号已递增。
        existing.setVersionId(current + 1L);
        if (declaredSize > 0) {
            existing.setFileSize(declaredSize);
        }
        if (contentMd5 != null && !contentMd5.isBlank()) {
            existing.setContentMd5(contentMd5);
            existing.setMd5Reliable(MD5_RELIABLE_CLIENT);
        }
        changeLog.record(spaceType, ownerId, EsFileChangeLog.EVENT_UPDATED, existing,
            existing.getFileId(), existing.getFilePath(), null);
    }

    /**
     * 版本号归一化：NULL（列上线前的存量行）按 0 处理。
     *
     * <p>0 的语义是**「还没有写过」**——存量行确实是这个状态（列上线前就存在、
     * 之后没有经过本方法的写路径）。首次登记会写 1（见 insertSafely），
     * 因此 0 与 n≥1 互斥，CAS 判定不会碰撞。
     */
    private static long normalizedVersion(EsFile row) {
        return row.getVersionId() == null ? 0L : row.getVersionId();
    }

    /**
     * 列表视图：一次查库同时给出「active 行索引」与「已软删路径集合」。
     *
     * <p>为什么要合成一次查询：列表是全站唯一出口（网盘页/挂载盘/快搜文件路都走它），
     * 每次列举都对同一空间查两遍库是白花；两条信息又都来自同一批行，天然该一起取。
     *
     * @param hiddenPaths 对用户不可见的路径集合（回收站等），供 hideDeleted 步骤过滤存储列举结果
     * @param activeByPath active 行按路径索引，供 reconcileAndMap 步骤回填 fileId 与可见性过滤
     */
    private record ListView(Map<String, EsFile> activeByPath, Set<String> hiddenPaths) {
    }

    /**
     * 一次列举的完整读侧结果：可见对象 + fileId 索引 + active 行索引。
     *
     * @param objects      过滤后对用户可见的对象（已剔除回收站路径）
     * @param fileIds      相对路径 → fileId（藏掉的行对应 null）
     * @param activeByPath active 行按路径索引（共享空间可见性过滤用）
     */
    public record Listing(List<DriveObject> objects, Map<String, Long> fileIds,
                          Map<String, EsFile> activeByPath,
                          Map<String, Long> versions) {
    }

    /**
     * **列举空间内容的唯一入口**——把「查库 → 藏掉软删 → 回填 fileId」一次做完。
     *
     * <p><b>为什么必须是唯一入口</b>：这三步的**顺序与配套关系是正确性的一部分**
     * （KI-10），拆成三个公开方法让调用方自己串，就等于把「漏一步」变成可能——
     * 而漏掉「藏」这一步的症状是「删了还在、还原永远冲突、空间永不释放」，
     * 全都不会报错。KI-9（`Seek` 漏 `(0, SeekStart)`）与 KI-10 是同一类错误：
     * **多个必须一起做的事，暴露成了多个可忘记的调用**。
     * 因此这里刻意不导出 {@code hideDeleted} / {@code reconcileAndMap}。
     *
     * @param objects 存储列举的原始结果（含回收站对象——软删不动 RustFS 对象）
     * @return 过滤 + 回填后的完整结果
     */
    public Listing list(String spaceType, Long ownerId, Long uploadBy, List<DriveObject> objects) {
        ListView view = listView(spaceType, ownerId);
        List<DriveObject> visible = hideDeleted(objects, view);
        Map<String, Long> fileIds = reconcileAndMap(spaceType, ownerId, uploadBy, visible, view);
        // 版本号一并透出：客户端下次写同一路径时要把它带回来做 CAS 校验
        // （路线图切片 1）。不给出的话客户端无从知道「我看到的是哪一版」。
        Map<String, Long> versions = new HashMap<>(view.activeByPath().size());
        view.activeByPath().forEach((path, row) -> versions.put(path, normalizedVersion(row)));
        return new Listing(visible, fileIds, view.activeByPath(), versions);
    }

    /**
     * 组装列表视图（active 索引 + 软删路径集合）。
     *
     * <p><b>两条口径必须同时成立</b>（KI-10）：
     * <ul>
     *   <li>非 active 行**不进** activeByPath——否则回填会以为它还在用，把它当既有行回填 fileId，
     *       用户看到的就是「删了还在」；</li>
     *   <li>非 active 路径**进** hiddenPaths——软删不动对象，存储列举里照样有这条对象，
     *       读侧必须按它把结果藏掉。</li>
     * </ul>
     * 同路径同时存在 active 与 recycled 行时（删完又重新上传过），
     * **以 active 为准**：那个对象键此刻属于 active 那份，列表必须照常显示它。
     *
     * <p>判定口径：**只有 active 与 state 为 NULL（列上线前的存量行）算在用**，
     * 其余一律按不可见处理。用白名单而非「等于 recycled 才隐藏」，
     * 是为了让将来新增的状态（如 purged）默认落到安全的一侧——
     * 漏藏一个状态是「删不掉」，漏出一行是「删了还在」且会污染还原，
     * 后者更贵。
     */
    private ListView listView(String spaceType, Long ownerId) {
        Map<String, EsFile> activeByPath = new HashMap<>();
        Set<String> hiddenPaths = new HashSet<>();
        for (EsFile row : rowsInSpace(spaceType, ownerId)) {
            String state = normalizedState(row);
            if (EsFile.STATE_ACTIVE.equals(state)) {
                activeByPath.put(row.getFilePath(), row);
            } else {
                hiddenPaths.add(row.getFilePath());
            }
        }
        // 同路径的 active 行覆盖掉 recycled 带来的隐藏——对象键此刻归 active 所有
        hiddenPaths.removeAll(activeByPath.keySet());
        return new ListView(activeByPath, hiddenPaths);
    }

    /**
     * 从存储列举结果里剔除已软删路径。
     *
     * <p>软删只改目录层、不动对象（回收站与在用文件共用同一个对象键），
     * 因此**存储列举结果里仍然有这条对象**。不过滤的话「删除」在用户眼里等于没发生。
     *
     * @return 过滤后的新列表（不改入参）
     */
    private static List<DriveObject> hideDeleted(List<DriveObject> objects, ListView view) {
        List<DriveObject> visible = new ArrayList<>(objects.size());
        for (DriveObject object : objects) {
            if (view.hiddenPaths().contains(normalizedOrNull(object.path()))) {
                continue;
            }
            visible.add(object);
        }
        return visible;
    }

    /**
     * 列表回填：为存储列举结果补 fileId；缺行的（存量/绕过登记写入）惰性补账。
     *
     * <p><b>回收站路径不得补账</b>（KI-10）：软删行的对象还在存储里，
     * 若这里 insert 一条新的 active 行，等于列表自己把删除撤销了，并连带堵死
     * 还原（原位置被自己占住，永远报冲突）与彻底删除（引用计数认为还有债主，
     * 对象永不释放）。
     *
     * @param view  {@link #listView} 的结果，复用同一次查询
     * @return path → fileId（与入参 objects 顺序一致的一一映射）
     */
    private Map<String, Long> reconcileAndMap(String spaceType, Long ownerId, Long uploadBy,
                                              List<DriveObject> objects, ListView view) {
        Map<String, EsFile> byPath = view.activeByPath();
        Map<String, Long> result = new HashMap<>(objects.size());
        for (DriveObject object : objects) {
            // 目录标记对象（键以 / 结尾）不补账：补一行 file_size=0 的「文件」会污染
            // 秒传判定（同哈希 0 字节文件会被误判已存在）与回收站语义（目录进了回收站
            // 还得给它补一套文件生命周期）。目录没有 fileId，客户端按尾斜杠识别即可。
            if (object.path().endsWith("/")) {
                result.put(object.path(), null);
                continue;
            }
            String path;
            try {
                path = DriveKeys.normalizeRelative(object.path());
            } catch (ServiceException ex) {
                // 存储里出现不合规键（历史脏数据）：照常返回，不补账
                log.warn("es_file 对账跳过不合规路径: {}", object.path());
                result.put(object.path(), null);
                continue;
            }
            EsFile row = byPath.get(path);
            if (row == null && !view.hiddenPaths().contains(path)) {
                row = insertSafely(spaceType, ownerId, uploadBy, path, object.size());
            }
            result.put(object.path(), row == null ? null : row.getFileId());
        }
        return result;
    }

    /**
     * 解析删除目标：fileId 优先（新链路），否则按路径。fileId 必须命中指定空间
     * 且归属校验通过，防止伪造他人 fileId 越权删文件。
     *
     * @return 规范化后的相对路径
     */
    public String resolveDeletePath(String spaceType, Long ownerId, Long fileId, String rawPath) {
        if (fileId != null) {
            EsFile row = fileMapper.selectById(fileId);
            // 只认 active 行：回收站里的行不能被"再删一次"（彻底删除走 purge 链路）
            if (row == null
                || !spaceType.equals(row.getSpaceType())
                || !ownerId.equals(row.getOwnerId())
                || !EsFile.STATE_ACTIVE.equals(normalizedState(row))) {
                throw new ServiceException("文件不存在或不属于当前空间");
            }
            return row.getFilePath();
        }
        return DriveKeys.normalizeRelative(rawPath);
    }

    // ── 回收站 + 版本（P1，wps-cloud-drive-benchmark.md §6-P1 第 1 项）──────────

    /**
     * 哈希可靠性来源：客户端整文件读算（对标钉盘 md5_reliable 语义）。
     */
    public static final String MD5_RELIABLE_CLIENT = "client";

    /**
     * 秒传查询：同空间内找一条**内容 MD5 相同**的在用行（切片 6）。
     *
     * <p>找到即意味着对象存储里已经有这份内容的对象——客户端不必再传字节，
     * 服务端用 {@code CopyObject} 把既有对象复制到目标键即可。
     * 前提是哈希可靠（{@code md5_reliable='client'}，客户端整文件读算），
     * 否则不同内容碰撞同一个 MD5 时秒传会**覆盖出错误内容**。
     *
     * @param contentMd5 十六进制小写 MD5；null/blank 直接返回 null（未上报不判秒传）
     */
    public EsFile findByContentHash(String spaceType, Long ownerId, String contentMd5) {
        if (contentMd5 == null || contentMd5.isBlank()) {
            return null;
        }
        return fileMapper.selectOne(new LambdaQueryWrapper<EsFile>()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId)
            .eq(EsFile::getContentMd5, contentMd5)
            .eq(EsFile::getMd5Reliable, MD5_RELIABLE_CLIENT)
            .eq(EsFile::getState, EsFile.STATE_ACTIVE)
            .last("limit 1"));
    }

    /**
     * 版本号递增点（同名覆盖上传唯一入口）。NULL 视为存量行（列上线前已登记），
     * 从 0 起算——首次覆盖后落到 1。
     */
    private static long bumpVersion(EsFile row) {
        return (row.getVersionId() == null ? 0L : row.getVersionId()) + 1L;
    }

    /**
     * 软删：目录行置 state='recycled'，保留 file_path 并快照 origin_path，
     * RustFS 对象不动（还原时同键复位，无需搬对象）。
     *
     * @return 被软删的行；行不存在返回 null（调用方照常删对象，幽灵行无需处理）
     */
    public EsFile markRecycled(String spaceType, Long ownerId, String path) {
        EsFile row = selectActiveByPath(spaceType, ownerId, path);
        if (row == null) {
            return null;
        }
        // 显式置 state：列上线前的存量行在库里是 NULL，置 recycled 必须先落成 active，
        // 否则 MyBatis-Plus 默认忽略 NULL 字段，行会带 NULL state 进回收站
        // （部分索引不收 NULL，restore 也查不到它）
        row.setState(EsFile.STATE_ACTIVE);
        fileMapper.updateById(row);
        row.setState(EsFile.STATE_RECYCLED);
        row.setDeletedAt(LocalDateTime.now());
        row.setOriginPath(row.getFilePath());
        fileMapper.updateById(row);
        changeLog.record(spaceType, ownerId, EsFileChangeLog.EVENT_DELETED, row,
            row.getFileId(), row.getFilePath(), null);
        return row;
    }

    /**
     * 回收站列表：当前空间的 recycled 行，按删除时间倒序（最近删除在前）。
     */
    public List<EsFile> listRecycled(String spaceType, Long ownerId) {
        return fileMapper.selectList(new LambdaQueryWrapper<EsFile>()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId)
            .eq(EsFile::getState, EsFile.STATE_RECYCLED)
            .orderByDesc(EsFile::getDeletedAt));
    }

    /**
     * 还原：按 origin_path 复位为 active。原路径已被同名 active 文件占用时报冲突，
     * 不自动覆盖（还原冲突由用户显式抉择：先删/改名现有文件再还原）。
     *
     * @return 复位后的行
     */
    public EsFile restoreFromRecycle(String spaceType, Long ownerId, Long fileId) {
        EsFile row = recycledOwned(spaceType, ownerId, fileId);
        String origin = row.getOriginPath() != null && !row.getOriginPath().isBlank()
            ? row.getOriginPath() : row.getFilePath();
        if (selectActiveByPath(spaceType, ownerId, origin) != null) {
            throw new ServiceException("原位置已有同名文件，请先处理现有文件后再还原");
        }
        // MyBatis-Plus 默认忽略 null 字段，setDeletedAt(null)/setOriginPath(null)
        // 不会落库——须用 UpdateWrapper 显式把两列清回 NULL，否则 active 行残留快照
        fileMapper.update(null, new LambdaUpdateWrapper<EsFile>()
            .eq(EsFile::getFileId, row.getFileId())
            .set(EsFile::getState, EsFile.STATE_ACTIVE)
            .set(EsFile::getFilePath, origin)
            .set(EsFile::getDeletedAt, null)
            .set(EsFile::getOriginPath, null));
        row.setState(EsFile.STATE_ACTIVE);
        row.setDeletedAt(null);
        row.setOriginPath(null);
        row.setFilePath(origin);
        changeLog.record(spaceType, ownerId, EsFileChangeLog.EVENT_RESTORED, row,
            row.getFileId(), row.getFilePath(), null);
        return row;
    }

    /**
     * 彻底删除：摘除目录行。
     * 只允许从回收站态删——active 行须先经软删，防止绕过回收站直删。
     *
     * <p><b>调用方是否该删对象，由返回值决定</b>——见 {@link PurgeResult}。这是 KI-7
     * 的修复：对象键由路径派生（{@code DriveKeys.keyAt}），而路径可能已被
     * 「删后重新上传」的新文件占用。此时旧行指向的那个键**已经属于新文件**，
     * 再按路径删对象就会把用户当前在用的内容删掉。
     *
     * @return 被摘除的行 + 是否可安全删除对象（{@link PurgeResult#deletable()}）
     */
    public PurgeResult purgeFromRecycle(String spaceType, Long ownerId, Long fileId) {
        EsFile row = recycledOwned(spaceType, ownerId, fileId);
        // 关键检查（KI-7）：**该对象键是否还被别的行引用**。
        //
        // 对象键由路径派生（DriveKeys.keyAt = 前缀 + 路径），所以「键归谁」
        // 等价于「还有没有别的 es_file 行指着同一 path」。
        //
        // ⚠️ 只查 active **不够**：软删的行同样持有这个路径，而唯一索引只约束
        // active（部分索引 WHERE state='active'），因此「两条 recycled 行同路径」
        // 是合法状态。若只查 active 就放行删除，A 行被删后 B 行还在指着那个键，
        // 用户对 B 点「还原」就会得到一个对象已不存在的幽灵文件。
        //
        // 故这里查**同路径的全部行**（不论状态），把自己排除。
        boolean referencedByOthers = fileMapper.exists(new LambdaQueryWrapper<EsFile>()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId)
            .eq(EsFile::getFilePath, row.getFilePath())
            .ne(EsFile::getFileId, row.getFileId()));
        fileMapper.deleteById(row.getFileId());
        changeLog.record(spaceType, ownerId, EsFileChangeLog.EVENT_PURGED, null,
            row.getFileId(), null, null);
        return new PurgeResult(row, !referencedByOthers);
    }

    /**
     * 彻底删除的结果。
     *
     * @param row       被摘除的回收站行
     * @param deletable 是否可以安全删除该行对应的对象；false 表示对象键已被
     *                  同路径的 active 文件占用（删了会丢用户当前数据）
     */
    public record PurgeResult(EsFile row, boolean deletable) {
    }

    /**
     * 取回收站内且归属校验通过的行；越权/非回收站态一律按"不存在"报。
     */
    private EsFile recycledOwned(String spaceType, Long ownerId, Long fileId) {
        EsFile row = fileId == null ? null : fileMapper.selectById(fileId);
        if (row == null
            || !spaceType.equals(row.getSpaceType())
            || !ownerId.equals(row.getOwnerId())
            || !EsFile.STATE_RECYCLED.equals(row.getState())) {
            throw new ServiceException("回收站中不存在该文件");
        }
        return row;
    }

    /**
     * 设置文档可见部门（文档级可见性片 2 数据面）。deptIds 空 = 恢复全体可见。
     * 仅共享空间文件有此语义；目录行不存在时报错。
     *
     * @return 更新后的逗号分隔部门串（空串=全体可见）
     */
    public String setRegisteredVisibleDepts(String spaceType, Long ownerId, Long operatorId,
                                            String path, List<Long> deptIds) {
        EsFile row = selectActiveByPath(spaceType, ownerId, path);
        if (row == null) {
            throw new ServiceException("文件不存在或未登记目录层");
        }
        // 操作者校验：个人空间=owner 本人；共享空间=上传者本人（上传者总能管理自己的文件）
        boolean allowed = EsSpace.TYPE_PERSONAL.equals(spaceType)
            ? ownerId.equals(row.getOwnerId())
            : operatorId != null && operatorId.equals(row.getUploadBy());
        if (!allowed) {
            throw new ServiceException("只有文件的上传者可以调整可见范围");
        }
        row.setVisibleDepts(serializeDepts(deptIds));
        fileMapper.updateById(row);
        return row.getVisibleDepts();
    }

    /**
     * 共享列表可见性过滤：visible_depts 为空的行全体可见；非空时仅
     * 用户所属部门命中或上传者本人可见（上传者总能看到自己传的文件）。
     *
     * @param uploaderOf path → 上传者（供"自己可见"判断；查询不到按 0 处理）
     */
    public List<DriveObject> filterSharedVisible(List<DriveObject> objects,
                                                 Map<String, EsFile> rowsByPath,
                                                 Long viewerDeptId, Long viewerUserId) {
        List<DriveObject> visible = new ArrayList<>(objects.size());
        for (DriveObject object : objects) {
            EsFile row = rowsByPath.get(object.path());
            if (sharedRowVisible(row, viewerDeptId, viewerUserId)) {
                visible.add(object);
            }
        }
        return visible;
    }

    /**
     * 共享空间单行可见性判据（列表过滤与下载授权共用同一份口径）。
     *
     * <p><b>两处必须共用</b>：列表负责「看不见」，下载接口负责「拿不到」。
     * 若只有列表过滤而下载不查，知道路径的人仍能直接签出 URL 拿走受限文件——
     * 「看不见」不等于「拿不到」。这是数据面授权，不是展示细节。
     *
     * @param row 目录行；null（未登记）按可见处理，与列表口径一致
     */
    public static boolean sharedRowVisible(EsFile row, Long viewerDeptId, Long viewerUserId) {
        if (row == null || row.getVisibleDepts() == null || row.getVisibleDepts().isBlank()) {
            return true;
        }
        // 上传者本人恒可见
        if (viewerUserId != null && Objects.equals(row.getUploadBy(), viewerUserId)) {
            return true;
        }
        return viewerDeptId != null && deptListContains(row.getVisibleDepts(), viewerDeptId);
    }

    /**
     * 下载授权校验：路径既不能在回收站里，也不能被文档级可见性挡在外面。
     *
     * <p>与 {@link #list} 共用同一份可见性判据（{@link #sharedRowVisible}），
     * 避免「列表能过滤、下载不过滤」的错位：软删只改目录层、对象仍在 RustFS，
     * **只要不校验就仍能下载**。
     *
     * <p><b>刻意不走 {@link #listView} 的全空间扫描</b>：下载是热路径，
     * 而且客户端每开一个文件、每次 Seek 回退重开流都会再签一次
     * （见 {@code internal/spacedav} 的 {@code readFile.reopen}）——
     * 拖一次视频进度条就是几十次。这里只需要**一个路径**的判定，
     * 走 {@code (space_type, owner_id, file_path)} 上的唯一索引做点查，
     * 与空间里有多少文件无关。
     *
     * <p>判据展开成两次点查（active 一次、任意状态一次）而不是「扫全表再查」：
     * ①先看该路径有没有 active 行——有就直接用它判可见性（这是绝大多数情况）；
     * ②没有 active 行时，再确认该路径是否存在**任何**行（即回收站行）→ 已删除。
     * 两次都是索引点查，最坏情况两次往返，与空间规模无关。
     *
     * @throws ServiceException 路径已删除或无权访问（不区分两者，避免探测）
     */
    public void assertDownloadable(String spaceType, Long ownerId, String path,
                                   Long viewerDeptId, Long viewerUserId) {
        String normalized = DriveKeys.normalizeRelative(path);
        // ① 有 active 行：直接按它判可见性
        EsFile active = selectActiveByPath(spaceType, ownerId, normalized);
        if (active != null) {
            if (EsSpace.TYPE_SHARED.equals(spaceType)
                && !sharedRowVisible(active, viewerDeptId, viewerUserId)) {
                throw new ServiceException("文件不存在或无权访问");
            }
            return;
        }
        // ② 没有 active 行：该路径存在任何行（回收站/purged）就是「已删除」。
        //    也涵盖「删完又重新上传」之外的幽灵行情形——一律拒绝，不区分两者以免探测。
        if (existsAnyRow(spaceType, ownerId, normalized)) {
            throw new ServiceException("文件不存在或已删除");
        }
        // ③ 目录层没有行（存量对象/历史脏数据）：不拦，与列表的「未登记按可见」一致。
        //    否则升级后那批惰性补账前的存量文件会集体下不动。
    }

    /**
     * 改名/移动的目录层动作：把 active 行的 file_path 改到新路径，**fileId 不变**。
     *
     * <p>这是「身份与路径解耦」的落点：改名只动属性，不动身份，
     * 因此分享链接、任务引用、知识索引里绑的都是 fileId 的那些都不会失效
     * （见 {@link EsFile} 类注与钉盘取证 §3）。
     *
     * <p>冲突判据：目标路径**已有 active 行**则拒绝，不覆盖（与还原冲突同一取向：
     * 破坏性选择交还用户，不替他决定）。
     *
     * @return 被移动的行；行不存在返回 null（历史脏数据，调用方照常放行）
     */
    public EsFile moveRegistered(String spaceType, Long ownerId, String fromPath, String toPath) {
        String from = DriveKeys.normalizeRelative(fromPath);
        String to = DriveKeys.normalizeRelative(toPath);
        EsFile row = selectActiveByPath(spaceType, ownerId, from);
        if (row == null) {
            return null;
        }
        EsFile occupied = selectActiveByPath(spaceType, ownerId, to);
        if (occupied != null && !Objects.equals(occupied.getFileId(), row.getFileId())) {
            throw new ServiceException("目标位置已有同名文件，未覆盖");
        }
        row.setFilePath(to);
        row.setFileName(fileNameOf(to));
        fileMapper.updateById(row);
        changeLog.record(spaceType, ownerId, EsFileChangeLog.EVENT_MOVED, row,
            row.getFileId(), row.getFilePath(), from);
        return row;
    }

    public static String serializeDepts(List<Long> deptIds) {
        if (deptIds == null || deptIds.isEmpty()) {
            return "";
        }
        return deptIds.stream().map(String::valueOf).collect(java.util.stream.Collectors.joining(","));
    }

    public static boolean deptListContains(String csv, long deptId) {
        for (String part : csv.split(",")) {
            if (part.trim().equals(String.valueOf(deptId))) {
                return true;
            }
        }
        return false;
    }

    /**
     * 按 fileId 查当前空间的 active 行（增量列举给变更补元数据用）。
     *
     * <p>只认 active：purged/deleted 行不应再把 size/时间带给客户端。
     */
    public EsFile selectActiveById(String spaceType, Long ownerId, Long fileId) {
        if (fileId == null) {
            return null;
        }
        return fileMapper.selectOne(new LambdaQueryWrapper<EsFile>()
            .eq(EsFile::getFileId, fileId)
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId)
            .eq(EsFile::getState, EsFile.STATE_ACTIVE));
    }

    /**
     * 按路径查稳定 fileId；目录行不存在返回 null（Upload Session 完成后回查用）。
     */
    public Long fileIdOfPath(String spaceType, Long ownerId, String path) {
        EsFile row = selectActiveByPath(spaceType, ownerId, path);
        return row == null ? null : row.getFileId();
    }

    /**
     * 删除文件后清理目录行；行不存在视为已清理。
     */
    public void deleteRegistered(String spaceType, Long ownerId, String path) {
        fileMapper.delete(new LambdaQueryWrapper<EsFile>()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId)
            .eq(EsFile::getFilePath, path));
    }

    /**
     * 当前空间的全部行（不论状态），一次查询取回。
     *
     * <p>刻意不带 state 条件：KI-10 需要同时知道「谁是 active」与「哪些路径已软删」，
     * 只查 active 会丢掉回收站那一半信息。列表出口一次列举只该查一次库。
     */
    private List<EsFile> rowsInSpace(String spaceType, Long ownerId) {
        return fileMapper.selectList(new LambdaQueryWrapper<EsFile>()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId));
    }

    /**
     * 规范化路径用于比对；不合规（历史脏数据）返回 null —— 调用方据此跳过过滤。
     */
    private static String normalizedOrNull(String raw) {
        try {
            return DriveKeys.normalizeRelative(raw);
        } catch (ServiceException ex) {
            return null;
        }
    }

    /**
     * 存量行 state 为 NULL（列上线前）时按 active 处理，读侧无感迁移。
     */
    private static String normalizedState(EsFile row) {
        return row.getState() == null ? EsFile.STATE_ACTIVE : row.getState();
    }

    /**
     * 按路径查 active 行（唯一索引已降为部分索引 WHERE state='active'，
     * 同一路径在回收站里可并存多行，故这里必须带 state 条件）。
     */
    private EsFile selectActiveByPath(String spaceType, Long ownerId, String path) {
        return fileMapper.selectOne(new LambdaQueryWrapper<EsFile>()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId)
            .eq(EsFile::getFilePath, path)
            .eq(EsFile::getState, EsFile.STATE_ACTIVE)
            .last("limit 1"));
    }

    /**
     * 该路径是否存在**任何**状态的行（含回收站）。
     *
     * <p>用于下载授权判定「这个路径是不是已经被软删了」——软删行仍占着 file_path，
     * 所以不能只看 active。用 exists 而非 selectList：只需要知道有没有，
     * 且能走 {@code (space_type, owner_id, file_path)} 索引。
     */
    private boolean existsAnyRow(String spaceType, Long ownerId, String path) {
        return fileMapper.exists(new LambdaQueryWrapper<EsFile>()
            .eq(EsFile::getSpaceType, spaceType)
            .eq(EsFile::getOwnerId, ownerId)
            .eq(EsFile::getFilePath, path));
    }

    /**
     * 插入；并发撞唯一键时按已存在行处理（返回已存在行），不让列表/签发失败。
     */
    private EsFile insertSafely(String spaceType, Long ownerId, Long uploadBy,
                                String path, long size) {
        return insertSafely(spaceType, ownerId, uploadBy, path, size, null);
    }

    /**
     * 插入（带内容哈希，切片 6）。哈希在插入时一并落行——秒传命中时没有后续字节写入，
     * 这是它唯一的落点。
     */
    private EsFile insertSafely(String spaceType, Long ownerId, Long uploadBy,
                                String path, long size, String contentMd5) {
        EsFile row = new EsFile();
        row.setSpaceType(spaceType);
        row.setOwnerId(ownerId);
        row.setUploadBy(uploadBy);
        row.setFilePath(path);
        row.setFileName(fileNameOf(path));
        row.setFileSize(Math.max(0L, size));
        if (contentMd5 != null && !contentMd5.isBlank()) {
            row.setContentMd5(contentMd5);
            row.setMd5Reliable(MD5_RELIABLE_CLIENT);
        }
        // 首次登记即「第 1 版」：让 0 与 1 有**互斥**含义。
        //
        // 这一行是 2026-10-16 实测逼出来的：原先新行 version_id 默认 0，
        // 于是「我期望版本 0（我认为文件还不存在）」与
        // 「文件存在且处于版本 0」**碰撞**——两个客户端都带 expectedVersion=0
        // 连续首次写入时，第二个不会被拒（实测确认）。
        // 现在语义唯一：0 = 不存在，n≥1 = 第 n 次写入。
        row.setVersionId(1L);
        try {
            fileMapper.insert(row);
            changeLog.record(spaceType, ownerId, EsFileChangeLog.EVENT_CREATED, row,
                row.getFileId(), row.getFilePath(), null);
            return row;
        } catch (DuplicateKeyException ex) {
            EsFile existing = selectActiveByPath(spaceType, ownerId, path);
            if (existing != null) {
                return existing;
            }
            throw ex;
        }
    }

    private static String fileNameOf(String path) {
        int index = path.lastIndexOf('/');
        return index < 0 ? path : path.substring(index + 1);
    }
}
