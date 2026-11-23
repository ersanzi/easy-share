import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'
import FileMemberEditor from '../FileMemberEditor.vue'

const api = vi.hoisted(() => ({ fileMemberList: vi.fn(), fileMemberReplace: vi.fn(), fileMemberCandidates: vi.fn() }))
vi.mock('../../services/core', () => ({ core: api }))
beforeEach(() => {
  vi.clearAllMocks()
  api.fileMemberList.mockResolvedValue([{ memberType: 'user', memberId: '9007199254740993', role: 'manage', memberName: '自己' }])
  api.fileMemberReplace.mockResolvedValue(undefined)
  api.fileMemberCandidates.mockResolvedValue([])
})
it('自己的成员行只读且保存空组时剔除自己', async () => {
  const wrapper = mount(FileMemberEditor, { props: { fileId: '100', fileName: '合同', operatorId: '9007199254740993' } })
  await flushPromises()
  expect(wrapper.find('select[aria-label="自己的角色"]').attributes('disabled')).toBeDefined()
  expect(wrapper.find('[data-testid="remove-member"]').attributes('disabled')).toBeDefined()
  await wrapper.find('[data-testid="save-members"]').trigger('click')
  await flushPromises()
  expect(api.fileMemberReplace).toHaveBeenCalledWith('100', [])
})
it('切换文件后迟到的成员请求不能覆盖新文件', async () => {
  let resolveOld!: (rows: unknown[]) => void
  api.fileMemberList.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
  const wrapper = mount(FileMemberEditor, { props: { fileId: 'old', fileName: '旧文件', operatorId: '7' } })
  await wrapper.setProps({ fileId: 'new', fileName: '新文件' })
  await flushPromises()
  resolveOld([{ memberType: 'user', memberId: '8', role: 'read', memberName: '旧文件成员' }])
  await flushPromises()
  expect(wrapper.text()).not.toContain('旧文件成员')
})
it('没有匹配成员时给出可恢复的搜索提示', async () => {
  const wrapper = mount(FileMemberEditor, { props: { fileId: '100', fileName: '合同', operatorId: '7' } })
  await flushPromises()
  await wrapper.get('input[aria-label="搜索成员或部门"]').setValue('不存在的成员')
  await wrapper.get('.file-member-search').trigger('submit')
  await flushPromises()
  expect(wrapper.text()).toContain('没有匹配的有效成员或部门，请换一个关键词')
})

it('空组保存失败后能重试，忙时防止重复提交', async () => {
  api.fileMemberList.mockResolvedValue([])
  api.fileMemberReplace.mockRejectedValueOnce(new Error('网络失败')).mockResolvedValueOnce(undefined)
  const wrapper = mount(FileMemberEditor, { props: { fileId: '100', fileName: '合同', operatorId: '7' } })
  await flushPromises()
  await wrapper.find('[data-testid="save-members"]').trigger('click')
  await flushPromises()
  expect(wrapper.text()).toContain('网络失败')
  expect(wrapper.find('[data-testid="save-members"]').attributes('disabled')).toBeUndefined()
  await wrapper.find('[data-testid="save-members"]').trigger('click')
  await flushPromises()
  expect(api.fileMemberReplace).toHaveBeenCalledTimes(2)
})
