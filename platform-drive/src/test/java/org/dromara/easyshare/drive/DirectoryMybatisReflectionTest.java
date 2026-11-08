package org.dromara.easyshare.drive;

import com.baomidou.mybatisplus.core.MybatisConfiguration;
import org.apache.ibatis.mapping.BoundSql;
import org.apache.ibatis.mapping.MappedStatement;
import org.apache.ibatis.mapping.ParameterMapping;
import org.apache.ibatis.reflection.DefaultReflectorFactory;
import org.apache.ibatis.reflection.MetaClass;
import org.apache.ibatis.reflection.Reflector;
import org.dromara.easyshare.drive.domain.EsFile;
import org.dromara.easyshare.drive.domain.EsFileChangeLog;
import org.dromara.easyshare.drive.mapper.EsFileChangeLogMapper;
import org.dromara.easyshare.drive.mapper.EsFileMapper;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.MethodSource;

import java.sql.PreparedStatement;
import java.util.Arrays;
import java.util.Map;
import java.util.stream.Stream;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

/**
 * 回归线上双 getter 故障：使用真实 Reflector、Mapper SQL 注入和参数处理器，不能只 mock Mapper。
 * 不连接生产数据库；仅 JDBC PreparedStatement 是记录绑定调用的替身。
 */
class DirectoryMybatisReflectionTest {

    static Stream<Object[]> directoryEntities() {
        return Stream.of(EsFile.class, EsFileChangeLog.class)
            .flatMap(type -> Stream.of(null, Boolean.FALSE, Boolean.TRUE)
                .map(value -> new Object[] {type, value}));
    }

    @ParameterizedTest
    @MethodSource("directoryEntities")
    void directoryHasUnambiguousBoxedGetterAndSetter(Class<?> type, Boolean value) throws Exception {
        Object entity = type.getDeclaredConstructor().newInstance();
        Reflector reflector = new Reflector(type);
        MetaClass metaClass = MetaClass.forClass(type, new DefaultReflectorFactory());
        assertTrue(metaClass.hasGetter("directory"));
        assertTrue(metaClass.hasSetter("directory"));
        assertEquals(Boolean.class, metaClass.getGetterType("directory"));
        assertEquals(Boolean.class, metaClass.getSetterType("directory"));
        reflector.getSetInvoker("directory").invoke(entity, new Object[] {value});
        // getGetterType 本身不会执行 AmbiguousMethodInvoker，必须真实调用才可抓住线上故障。
        assertEquals(value, reflector.getGetInvoker("directory").invoke(entity, new Object[0]));
        MybatisConfiguration configuration = new MybatisConfiguration();
        assertEquals(value, configuration.newMetaObject(entity).getValue("directory"));
        assertEquals(1, Arrays.stream(type.getMethods())
            .filter(method -> method.getParameterCount() == 0)
            .filter(method -> method.getName().equals("getDirectory")
                || method.getName().equals("isDirectory")).count());
    }

    @ParameterizedTest
    @MethodSource("directoryEntities")
    void realMapperInsertAndUpdateBindEntityParameters(Class<?> type, Boolean value) throws Exception {
        MybatisConfiguration configuration = new MybatisConfiguration();
        Class<?> mapperType;
        Object entity;
        if (type == EsFile.class) {
            EsFile file = new EsFile();
            file.setFileId(101L);
            file.setFilePath("docs/readme.txt");
            file.setDirectory(value);
            entity = file;
            mapperType = EsFileMapper.class;
        } else {
            EsFileChangeLog log = new EsFileChangeLog();
            log.setLogId(102L);
            log.setFilePath("docs/readme.txt");
            log.setDirectory(value);
            entity = log;
            mapperType = EsFileChangeLogMapper.class;
        }
        // 独立于 Spring 启动，显式补齐 MP 的真实主键生成器。
        com.baomidou.mybatisplus.core.toolkit.GlobalConfigUtils.getGlobalConfig(configuration)
            .setIdentifierGenerator(new com.baomidou.mybatisplus.core.incrementer.DefaultIdentifierGenerator());
        // 真正注册产品 Mapper，由 MyBatis-Plus 注入 insert/updateById，而非测试自建 SQL。
        configuration.addMapper(mapperType);
        for (String operation : new String[] {"insert", "updateById"}) {
            Object parameter = operation.equals("insert") ? entity : Map.of("et", entity);
            MappedStatement statement = configuration.getMappedStatement(mapperType.getName() + "." + operation);
            BoundSql boundSql = statement.getBoundSql(parameter);
            assertTrue(boundSql.getSql().contains("file_path"));
            PreparedStatement jdbc = mock(PreparedStatement.class);
            configuration.newParameterHandler(statement, parameter, boundSql).setParameters(jdbc);
            boolean directoryBound = false;
            boolean filePathBound = false;
            for (int i = 0; i < boundSql.getParameterMappings().size(); i++) {
                ParameterMapping mapping = boundSql.getParameterMappings().get(i);
                String property = mapping.getProperty();
                if (property.equals("directory") || property.equals("et.directory")) {
                    directoryBound = true;
                    assertNotNull(value);
                    verify(jdbc).setBoolean(i + 1, value);
                }
                if (property.equals("filePath") || property.equals("et.filePath")) {
                    filePathBound = true;
                    verify(jdbc).setString(i + 1, "docs/readme.txt");
                }
            }
            assertTrue(filePathBound, "所有实体参数必须实际经过 MyBatis 属性读取");
            assertEquals(value != null, directoryBound, "null 保持既有 NOT_NULL 字段策略");
        }
    }

    @Test
    void directoryHelperKeepsNullAsFalseWithoutBecomingBeanGetter() {
        EsFile file = new EsFile();
        assertFalse(file.directoryEntry());
        file.setDirectory(false);
        assertFalse(file.directoryEntry());
        file.setDirectory(true);
        assertTrue(file.directoryEntry());
    }
}
