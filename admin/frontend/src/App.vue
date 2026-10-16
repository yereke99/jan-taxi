<script setup lang="ts">
import { ConfirmHost, ToastHost } from './components'
</script>

<template>
  <RouterView />
  <ToastHost />
  <ConfirmHost />
</template>
