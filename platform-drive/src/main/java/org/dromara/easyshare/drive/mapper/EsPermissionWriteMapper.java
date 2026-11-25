package org.dromara.easyshare.drive.mapper;

import org.apache.ibatis.annotations.Insert;
import org.apache.ibatis.annotations.Param;
import org.apache.ibatis.annotations.Select;
import java.util.List;

/** 低频授权写操作共用数据库事务锁；多进程部署也不能交错执行整组替换。 */
public interface EsPermissionWriteMapper {
    @Select("SELECT guard_id FROM es_permission_write_guard WHERE guard_id = 1 FOR UPDATE NOWAIT")
    Integer lockGuard();

    /** 共享锁保护范围检查，允许并发读取；冲突立即返回重试，避免长事务拖住整个授权队列。 */
    @Select("SELECT dept_id FROM sys_dept ORDER BY dept_id FOR SHARE NOWAIT")
    List<Long> lockDepartments();

    @Select("""
        <script>
        SELECT user_id FROM sys_user WHERE user_id IN
        <foreach collection="ids" item="id" open="(" separator="," close=")">#{id}</foreach>
        ORDER BY user_id FOR SHARE NOWAIT
        </script>
        """)
    List<Long> lockUsers(@Param("ids") List<Long> ids);

    @Insert("""
        INSERT INTO es_permission_audit(actor_id, action, target_id, before_state, after_state, create_time)
        VALUES(#{actorId}, #{action}, #{targetId}, #{before}, #{after}, CURRENT_TIMESTAMP)
        """)
    int insertAudit(@Param("actorId") Long actorId, @Param("action") String action,
                    @Param("targetId") String targetId, @Param("before") String before,
                    @Param("after") String after);
}
