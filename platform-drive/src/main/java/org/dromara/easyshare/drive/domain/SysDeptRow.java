package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;

/**
 * RuoYi 系统部门表的只读投影（管理页部门授权下拉的数据源）。
 *
 * @author EasyShare
 */
@Data
@TableName("sys_dept")
public class SysDeptRow {

    private Long deptId;

    private Long parentId;

    private String deptName;

    /** 0 正常、1 停用 */
    private String status;

    /** 0 存在、2 删除（RuoYi 逻辑删除） */
    private String delFlag;
}
