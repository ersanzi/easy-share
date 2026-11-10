package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.test.util.ReflectionTestUtils;
import software.amazon.awssdk.awscore.exception.AwsErrorDetails;
import software.amazon.awssdk.services.s3.S3AsyncClient;
import software.amazon.awssdk.services.s3.model.HeadObjectRequest;
import software.amazon.awssdk.services.s3.model.HeadObjectResponse;
import software.amazon.awssdk.services.s3.model.S3Exception;

import java.util.concurrent.CompletableFuture;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

/** 精确区分缺对象和权限/服务异常，不将错误吞成不存在。 */
class DriveStorageMetadataTest {
    private DriveStorage storage;
    private S3AsyncClient client;

    @BeforeEach
    void setUp() {
        DriveProperties properties = new DriveProperties();
        properties.setBucket("isolated-test-bucket");
        storage = new DriveStorage(properties);
        client = mock(S3AsyncClient.class);
        ReflectionTestUtils.setField(storage, "client", client);
    }

    @Test
    void readsEmptyObjectMetadataAndStripsETagQuotes() {
        when(client.headObject(any(HeadObjectRequest.class))).thenReturn(CompletableFuture.completedFuture(
            HeadObjectResponse.builder().contentLength(0L).eTag("\"d41d8cd98f00b204e9800998ecf8427e\"").build()));
        var metadata = storage.metadataAt("users/1/", "empty.txt");
        assertTrue(metadata.matches(0, "d41d8cd98f00b204e9800998ecf8427e"));
        assertFalse(metadata.matches(1, "d41d8cd98f00b204e9800998ecf8427e"));
        var request = org.mockito.ArgumentCaptor.forClass(HeadObjectRequest.class);
        verify(client).headObject(request.capture());
        assertEquals("users/1/empty.txt", request.getValue().key());
        assertEquals("isolated-test-bucket", request.getValue().bucket());
    }

    @Test
    void missingObjectOnlyReturnsNull() {
        for (String code : new String[]{"NoSuchKey", "NotFound", "404"}) {
            when(client.headObject(any(HeadObjectRequest.class))).thenReturn(CompletableFuture.failedFuture(
                S3Exception.builder().statusCode(404)
                    .awsErrorDetails(AwsErrorDetails.builder().errorCode(code).build()).build()));
            assertNull(storage.metadataAt("users/1/", "missing.txt"));
        }
    }

    @Test
    void accessDeniedAndMissingBucketDoNotBecomeMissingObject() {
        for (var failure : new RuntimeException[]{
            S3Exception.builder().statusCode(403).message("denied").build(),
            S3Exception.builder().statusCode(404).awsErrorDetails(AwsErrorDetails.builder().errorCode("NoSuchBucket").build()).build(),
            S3Exception.builder().statusCode(503).message("unavailable").build()}) {
            when(client.headObject(any(HeadObjectRequest.class))).thenReturn(CompletableFuture.failedFuture(failure));
            assertThrows(ServiceException.class, () -> storage.metadataAt("users/1/", "file.txt"));
        }
    }

    @Test
    void networkFailureIsNotMissingObject() {
        when(client.headObject(any(HeadObjectRequest.class))).thenReturn(CompletableFuture.failedFuture(
            new IllegalStateException("network failed")));
        assertThrows(ServiceException.class, () -> storage.metadataAt("users/1/", "file.txt"));
    }
}
