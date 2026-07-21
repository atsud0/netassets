<script setup lang="ts">
const stats = [
  { label: '子公司', value: '—', hint: '组织机构待初始化' },
  { label: '业务系统', value: '—', hint: '含核心与重要系统' },
  { label: '网络资产', value: '—', hint: '域名、IP、URL与服务' },
  { label: '待处理', value: '—', hint: '未归属、重复与审核事项' },
]
</script>

<template>
  <section>
    <div class="page-heading">
      <div><span class="eyebrow">OVERVIEW</span><h1>集团工作台</h1><p>统一掌握各子公司的业务系统、网络资产和填报进度。</p></div>
      <el-button type="primary">创建填报任务</el-button>
    </div>
    <div class="stat-grid">
      <article v-for="stat in stats" :key="stat.label" class="stat-card">
        <span>{{ stat.label }}</span><strong>{{ stat.value }}</strong><small>{{ stat.hint }}</small>
      </article>
    </div>
    <div class="content-grid">
      <article class="panel"><div class="panel-header"><div><h2>填报进度</h2><p>当前没有进行中的集团填报任务。</p></div></div><div class="empty-state">完成组织初始化后，可在此下发季度或专项盘点任务。</div></article>
      <article class="panel"><div class="panel-header"><div><h2>数据质量</h2><p>关注未归属、重复和敏感数据系统。</p></div></div><ul class="metric-list"><li><span>未关联业务系统资产</span><strong>—</strong></li><li><span>疑似重复资产</span><strong>—</strong></li><li><span>公网且涉及敏感个人信息</span><strong>—</strong></li></ul></article>
    </div>
  </section>
</template>
