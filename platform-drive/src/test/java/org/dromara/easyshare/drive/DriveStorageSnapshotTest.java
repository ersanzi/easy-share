package org.dromara.easyshare.drive;

import org.dromara.common.core.exception.ServiceException;
import org.dromara.easyshare.drive.domain.EsFile;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.test.util.ReflectionTestUtils;
import software.amazon.awssdk.core.ResponseInputStream;
import software.amazon.awssdk.core.async.AsyncResponseTransformer;
import software.amazon.awssdk.services.s3.S3AsyncClient;
import software.amazon.awssdk.services.s3.model.GetObjectRequest;
import software.amazon.awssdk.services.s3.model.GetObjectResponse;

import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.security.MessageDigest;
import java.util.HexFormat;
import java.util.concurrent.CompletableFuture;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

/** 仅纯流和 mock：无真实 S3、数据库或硬编码摘要。 */
class DriveStorageSnapshotTest {
    private static final String PREFIX = "users/1/";
    private DriveStorage storage;
    private S3AsyncClient client;

    @BeforeEach
    void setUp() {
        DriveProperties properties = new DriveProperties();
        properties.setBucket("isolated-test-bucket");
        storage = spy(new DriveStorage(properties));
        client = mock(S3AsyncClient.class);
        ReflectionTestUtils.setField(storage, "client", client);
    }

    @Test
    void digestEmptyAndMultiMegabyteStreamsWithBoundedShortReads() throws Exception {
        for (int size : new int[]{0, 3 * 1024 * 1024 + 17}) {
            MessageDigest expected = MessageDigest.getInstance("SHA-256");
            for (int i = 0; i < size; i++) expected.update((byte) i);
            InputStream input = new InputStream() {
                private int offset;
                public int read() { return offset < size ? offset++ & 255 : -1; }
                public int read(byte[] b, int off, int len) throws IOException {
                    assertTrue(len <= 64 * 1024, "不得请求整文件缓冲");
                    return super.read(b, off, Math.min(len, 4093));
                }
            };
            assertEquals(HexFormat.of().formatHex(expected.digest()), DriveStorage.digestInput(input));
        }
    }

    private void respond(InputStream input) {
        when(client.<ResponseInputStream<GetObjectResponse>>getObject(any(GetObjectRequest.class), any(AsyncResponseTransformer.class)))
            .thenReturn(CompletableFuture.completedFuture(new ResponseInputStream<>(GetObjectResponse.builder().build(), input)));
    }

    private static String hash(byte[] content) throws Exception {
        return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(content));
    }

    @Test
    void sha256UsesScopedRequestAndClosesResponse() throws Exception {
        byte[] content = {0, 1, 2, 3};
        InputStream input = spy(new ByteArrayInputStream(content));
        respond(input);
        assertEquals(hash(content), storage.sha256At(PREFIX, "file.bin"));
        verify(input).close();
        verify(client).getObject(eq(GetObjectRequest.builder().bucket("isolated-test-bucket")
            .key(PREFIX + "file.bin").build()), any(AsyncResponseTransformer.class));
    }

    @Test
    void readAndCloseFailuresCloseStreamAndDoNotExposeCredentials() throws Exception {
        for (boolean closeFailure : new boolean[]{false, true}) {
            InputStream input = mock(InputStream.class);
            when(input.read(any(byte[].class), anyInt(), anyInt())).thenReturn(-1);
            if (closeFailure) doThrow(new IOException("secretKey=sensitive")).when(input).close();
            else when(input.read(any(byte[].class), anyInt(), anyInt())).thenThrow(new IOException("secretKey=sensitive"));
            respond(input);
            var error = assertThrows(ServiceException.class, () -> storage.sha256At(PREFIX, "file.bin"));
            assertTrue(error.getMessage().contains("SHA-256"));
            assertFalse(error.getMessage().contains("sensitive"));
            verify(input).close();
        }
    }

    @Test
    void sdkFailureIsReadableAndSanitized() {
        when(client.<ResponseInputStream<GetObjectResponse>>getObject(any(GetObjectRequest.class), any(AsyncResponseTransformer.class)))
            .thenReturn(CompletableFuture.failedFuture(new IllegalStateException("accessKey=sensitive")));
        var error = assertThrows(ServiceException.class, () -> storage.sha256At(PREFIX, "file.bin"));
        assertTrue(error.getMessage().contains("SHA-256"));
        assertFalse(error.getMessage().contains("sensitive"));
    }

    @Test
    void snapshotContentIgnoresEtagButRequiresHashAndSize() throws Exception {
        doReturn(new DriveStorage.ObjectMetadata(1, "multipart-2")).when(storage).metadataAt(PREFIX, "source");
        doReturn(new DriveStorage.ObjectMetadata(1, "copy-etag")).when(storage).metadataAt(PREFIX, "target");
        doReturn(hash(new byte[]{1})).when(storage).sha256At(PREFIX, "source");
        doReturn(hash(new byte[]{1})).when(storage).sha256At(PREFIX, "target");
        assertTrue(storage.sameSnapshotContent(PREFIX, "source", "target"));
        doReturn(new DriveStorage.ObjectMetadata(1, "multipart-2")).when(storage).metadataAt(PREFIX, "target");
        doReturn(hash(new byte[]{2})).when(storage).sha256At(PREFIX, "target");
        assertFalse(storage.sameSnapshotContent(PREFIX, "source", "target"));
        for (var metadata : new DriveStorage.ObjectMetadata[]{null, new DriveStorage.ObjectMetadata(2, "multipart-2")}) {
            doReturn(metadata).when(storage).metadataAt(PREFIX, "target");
            clearInvocations(storage);
            assertFalse(storage.sameSnapshotContent(PREFIX, "source", "target"));
            verify(storage, never()).sha256At(anyString(), anyString());
        }
    }

    @Test
    void legacySnapshotHashRemainsNullWithoutReadyState() {
        EsFile legacy = new EsFile();
        assertNull(legacy.getRecycleSnapshotSha256());
        assertNull(legacy.getRecycleSnapshotState());
    }
}
