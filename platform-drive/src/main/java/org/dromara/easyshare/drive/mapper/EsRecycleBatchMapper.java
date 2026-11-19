package org.dromara.easyshare.drive.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import org.apache.ibatis.annotations.Param;
import org.apache.ibatis.annotations.Select;
import org.dromara.easyshare.drive.domain.EsRecycleBatch;

/** 回收站批次 Mapper；幂等唯一约束由 es_recycle_batch 索引保证。 */
public interface EsRecycleBatchMapper extends BaseMapper<EsRecycleBatch> {
    /** 与目录保护触发器相同的空间锁；先拿锁再核对身份和对象，避免新建/恢复抢占。 */
    @Select("SELECT 1 FROM pg_advisory_xact_lock(hashtextextended('es_file:' || #{space} || ':' || CAST(#{owner} AS text), 0))")
    Integer lockSpace(@Param("space") String space, @Param("owner") Long owner);
}
