package org.dromara.easyshare.drive.service;

import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.easyshare.drive.DriveStorage;
import org.springframework.stereotype.Service;

/**
 * 改名/移动的服务端编排（切片 4）。
 *
 * <p><b>为什么单独成一个服务，而不是写在控制器里</b>：这段逻辑有**补偿步骤**，
 * 而补偿是那种「平时不执行、出事才执行」的代码——写在控制器里就只能靠人眼审查，
 * 写在这里才能被单测直接钉住（控制器依赖 {@code LoginHelper} 静态方法，单测起不来）。
 * 出事才走的代码更需要测试，因为平时它永远不被执行。
 *
 * @author EasyShare
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class DriveMoveService {

    private final DriveStorage storage;
    private final DriveFileService fileService;

    /**
     * 改名/移动：目录行改路径 → 服务端复制对象 → 删源对象。
     *
     * <p><b>顺序为什么是「行先、对象后」</b>：目录行的唯一索引
     * （{@code uk_es_file_path}，部分索引只约束 active）是**唯一可靠的并发闸门**。
     * 反过来先复制对象的话，复制出来又还没登记的那个瞬间，一次普通的列表请求
     * 就会按「存储里有、目录层没有」把它惰性补账成一条**新行 + 新 fileId**——
     * 于是真正的目录行再想改过去就撞唯一键了。所以先占住名字。
     *
     * <p><b>代价与补偿</b>：行先改，就存在「行已改、对象还没搬」的中间态。
     * 若此时复制失败，光抛错会留下两处坏状态：
     * <ol>
     *   <li>新路径有一条**幽灵行**（登记了但没有对象，点开 404）；</li>
     *   <li>旧路径的对象还在，但没有行指向它——下一次列表会把它惰性补账成
     *       **新 fileId**，于是旧 fileId 的引用（分享链接、任务引用、知识索引）
     *       全部失效。这恰恰是本服务要解决的 KI-12 问题本身。</li>
     * </ol>
     * 所以失败时必须**把行挪回原位**，让状态回到「行与对象都在原路径」这个一致点；
     * 并尽力删掉可能已经复制出去的副本（复制成功、删源失败的情形），
     * 否则那份副本同样会被列表补账成一条重复文件。
     *
     * @param prefix 空间前缀（调用方已按权限校验产出）
     * @throws RuntimeException 原样抛出存储层错误；补偿失败只记日志，不掩盖原始错误
     */
    public void move(String spaceType, Long ownerId, String prefix, String from, String to) {
        // 先在目录层占住目标名字：目标已被占用时在这里就拒绝，不会有任何对象被复制
        fileService.moveRegistered(spaceType, ownerId, from, to);
        try {
            storage.copyAt(prefix, from, to);
            storage.deleteAt(prefix, from);
        } catch (RuntimeException ex) {
            compensate(spaceType, ownerId, prefix, from, to);
            throw ex;
        }
    }

    /**
     * 失败补偿：行挪回原位 + 尽力清掉目标处的副本。
     *
     * <p>顺序刻意是「先挪行、再删副本」：行挪回去之后状态已经一致（行与对象都在原路径），
     * 删副本只是清理多余数据。反过来先删副本的话，中途异常会留下「行在新路径、
     * 对象也没了」——那才是真的丢数据。
     *
     * <p>补偿本身失败不再上抛：原始错误（配额/权限/存储故障）对用户更有诊断价值，
     * 用一个补偿异常把它盖掉只会让人查错方向。但要留下 error 日志——补偿失败意味着
     * 数据可能处于不一致态，需要人工介入。
     */
    private void compensate(String spaceType, Long ownerId, String prefix, String from, String to) {
        try {
            fileService.moveRegistered(spaceType, ownerId, to, from);
        } catch (RuntimeException rollbackEx) {
            log.error("改名补偿失败：目录行未能从 {} 挪回 {}（space={} owner={}），"
                    + "该行现指向一个不存在的对象，需要人工核对",
                to, from, spaceType, ownerId, rollbackEx);
            return;
        }
        try {
            storage.deleteAt(prefix, to);
        } catch (RuntimeException cleanupEx) {
            log.error("改名补偿：目录行已挪回 {}，但目标处的副本 {} 未能清除，"
                    + "下次列表可能把它补账成重复文件（space={} owner={}）",
                from, to, spaceType, ownerId, cleanupEx);
        }
    }
}
