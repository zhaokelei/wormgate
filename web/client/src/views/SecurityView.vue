<script setup>
/**
 * Client console → Security.
 *
 * This password only unlocks the browser UI on 127.0.0.1:7200. The wg_ token
 * that authenticates fpc to fps lives on the Config page and is untouched by
 * anything on this page.
 */
import TopBar from '@common/ui/TopBar.vue'
import PageHead from '@common/ui/PageHead.vue'
import PasswordForm from '@common/ui/PasswordForm.vue'

import { clientApi } from '../api'
import { live } from '../store'
import { state } from '../session'
</script>

<template>
  <TopBar title="安全" subtitle="控制台登录密码" :live-state="live.link" :version="state.meta?.version" />

  <div class="wg-page">
    <PageHead title="修改登录密码" description="仅用于打开本机客户端控制台，不影响隧道连接。">
      <template #actions>
        <span class="wg-badge outline">127.0.0.1:7200</span>
      </template>
    </PageHead>

    <PasswordForm :submit="clientApi.changePassword" />

    <div class="wg-card">
      <div class="wg-card-body pw-notes">
        <h2>凭据分工</h2>
        <p>本地登录密码（本页）只用于打开这台机器上的客户端控制台 UI。首次启动由 fpc 打印一次，存于 data/client-console-password。</p>
        <p>服务端令牌 wg_... 用于 fpc 连接 fps 控制面，在「配置」页填写或由管理台签发。改本页密码不会轮换、也不会影响这个令牌。</p>
        <p>修改后的密码以 argon2id 哈希写入配置文件的 client.webui.password_hash（文件权限 0600），重启后仍然有效；启动参数 --webui-password 与旧密码随即失效。其他已登录的浏览器会被立即登出，当前页面保持登录。</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.pw-notes { display: flex; flex-direction: column; gap: 10px; max-width: 88ch; }
.pw-notes h2 { font-size: var(--fs-13); font-weight: 600; margin: 0; }
.pw-notes p { font-size: var(--fs-12); line-height: 1.7; color: var(--text-dim); margin: 0; }
.pw-notes strong { color: var(--text); font-weight: 600; }
</style>
