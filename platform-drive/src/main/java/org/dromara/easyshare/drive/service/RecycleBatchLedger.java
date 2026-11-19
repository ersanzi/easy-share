package org.dromara.easyshare.drive.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.EsRecycleBatch;
import org.dromara.easyshare.drive.mapper.EsRecycleBatchMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Propagation;
import org.springframework.transaction.annotation.Transactional;

/** 操作账独立提交：对象存储与目录事务不原子，异常或重启后仍能查回原删除批次。 */
@Service
@RequiredArgsConstructor
public class RecycleBatchLedger {
    private final EsRecycleBatchMapper batches;
    private final tools.jackson.databind.json.JsonMapper json;

    public String encodeMembers(java.util.List<org.dromara.easyshare.drive.domain.EsFile> members) {
        return json.writeValueAsString(members);
    }

    public java.util.List<org.dromara.easyshare.drive.domain.EsFile> decodeMembers(String value) {
        if (value == null || value.isBlank()) { throw new ServiceException("删除批次缺少准确成员记录"); }
        return new java.util.ArrayList<>(java.util.Arrays.asList(json.readValue(value, org.dromara.easyshare.drive.domain.EsFile[].class)));
    }

    @Transactional(propagation = Propagation.REQUIRES_NEW, rollbackFor = Exception.class)
    public EsRecycleBatch reserve(EsRecycleBatch batch) {
        EsRecycleBatch existing = batches.selectOne(new LambdaQueryWrapper<EsRecycleBatch>()
            .eq(EsRecycleBatch::getSpaceType, batch.getSpaceType()).eq(EsRecycleBatch::getOwnerId, batch.getOwnerId())
            .eq(EsRecycleBatch::getOperatorId, batch.getOperatorId()).eq(EsRecycleBatch::getOperationId, batch.getOperationId()));
        if (existing != null) { return existing; }
        if (batches.insert(batch) != 1) { throw new ServiceException("回收站操作账写入失败"); }
        return batch;
    }

    @Transactional(propagation = Propagation.REQUIRES_NEW, rollbackFor = Exception.class)
    public void checkpoint(EsRecycleBatch batch) {
        if (batches.updateById(batch) != 1) { throw new ServiceException("回收站操作状态保存失败"); }
    }
}
