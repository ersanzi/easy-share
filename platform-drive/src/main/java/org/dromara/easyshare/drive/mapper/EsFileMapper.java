package org.dromara.easyshare.drive.mapper;

import org.dromara.common.mybatis.core.mapper.BaseMapperPlus;
import org.dromara.easyshare.drive.domain.EsFile;

/**
 * 文件目录层 Mapper。
 *
 * @author EasyShare
 */
public interface EsFileMapper extends BaseMapperPlus<EsFile, EsFile> {
    /** ACL 修改与文件删除、移动串行，锁顺序始终在权限写保护之后。 */
    @org.apache.ibatis.annotations.Select("SELECT file_id FROM es_file WHERE file_id = #{fileId} FOR UPDATE")
    Long lockForPermissionUpdate(@org.apache.ibatis.annotations.Param("fileId") Long fileId);
}
