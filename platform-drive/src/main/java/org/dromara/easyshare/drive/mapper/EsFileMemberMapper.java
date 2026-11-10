package org.dromara.easyshare.drive.mapper;

import org.apache.ibatis.annotations.Delete;
import org.apache.ibatis.annotations.Insert;
import org.apache.ibatis.annotations.Param;
import org.apache.ibatis.annotations.Select;
import org.apache.ibatis.annotations.Update;
import org.dromara.easyshare.drive.domain.EsFileMember;

import java.util.List;

/**
 * 文档级成员角色 Mapper（{@code es_file_member}，复合主键）。
 * <p>
 * 与 {@link EsUserRoleMapper} 同款取舍：复合主键不进 MyBatis-Plus 实体，
 * 读写全部显式 SQL——这张表的操作只有「按文件取行 / 按主体取行 / 整组替换 / 单行删」四种。
 *
 * @author EasyShare
 */
public interface EsFileMemberMapper {

    /** 某文件的全部成员角色行（管理面板展示用） */
    @Select("SELECT file_id, member_type, member_id, role FROM es_file_member WHERE file_id = #{fileId} ORDER BY member_type, member_id")
    List<EsFileMember> selectByFile(@Param("fileId") Long fileId);

    /** 某账号的用户行（按主体取，列表过滤一次拉齐） */
    @Select("SELECT file_id, member_type, member_id, role FROM es_file_member WHERE member_type = 'user' AND member_id = #{userId}")
    List<EsFileMember> selectUserRowsOf(@Param("userId") Long userId);

    /** 某部门的部门行 */
    @Select("SELECT file_id, member_type, member_id, role FROM es_file_member WHERE member_type = 'dept' AND member_id = #{deptId}")
    List<EsFileMember> selectDeptRowsOf(@Param("deptId") Long deptId);

    @Select("SELECT role FROM es_file_member WHERE file_id = #{fileId} AND member_type = #{memberType} AND member_id = #{memberId}")
    String selectRole(@Param("fileId") Long fileId, @Param("memberType") String memberType, @Param("memberId") Long memberId);

    @Insert("INSERT INTO es_file_member(file_id, member_type, member_id, role) VALUES(#{fileId}, #{memberType}, #{memberId}, #{role})")
    int insertRow(@Param("fileId") Long fileId, @Param("memberType") String memberType,
                  @Param("memberId") Long memberId, @Param("role") String role);

    @Update("UPDATE es_file_member SET role = #{role} WHERE file_id = #{fileId} AND member_type = #{memberType} AND member_id = #{memberId}")
    int updateRole(@Param("fileId") Long fileId, @Param("memberType") String memberType,
                   @Param("memberId") Long memberId, @Param("role") String role);

    @Delete("DELETE FROM es_file_member WHERE file_id = #{fileId} AND member_type = #{memberType} AND member_id = #{memberId}")
    int deleteRow(@Param("fileId") Long fileId, @Param("memberType") String memberType, @Param("memberId") Long memberId);

    /** 文件删除时随行清理（防孤儿行） */
    @Delete("DELETE FROM es_file_member WHERE file_id = #{fileId}")
    int deleteByFile(@Param("fileId") Long fileId);
}
